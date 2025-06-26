/*
Copyright 2025 amazee.io

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package task

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	"github.com/mholt/archives"
	"github.com/uselagoon/machinery/api/lagoon"
	lclient "github.com/uselagoon/machinery/api/lagoon/client"
	"github.com/uselagoon/machinery/utils/sshtoken"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// version/build information (populated at build time by make file)
var (
	TaskVersion = "0.x.x"
)

type TaskArgs struct {
	BackupId      string `json:"backup_id"`
	RestoreFilter string `json:"restore_path"`
}

type RestoreTask struct {
	// Config provided by advanced task.
	Args TaskArgs

	// Config needed for operation.
	Ctx            context.Context
	K8sConfig      rest.Config
	Client         client.Client
	WatchingClient client.WithWatch
	Clientset      kubernetes.Clientset
	TaskId         string
	TaskKey        string
	TokenHost      string
	TokenPort      string
	APIHost        string
}

func NewRestoreTask(
	backupId string,
	restoreFilter string,
	k8sConfig *rest.Config,
	namespace string,
	taskId string,
	tokenHost string,
	tokenPort string,
	apiHost string,
) (*RestoreTask, error) {
	// Create a schema with k8up resources.
	var clientScheme = runtime.NewScheme()
	_ = scheme.AddToScheme(clientScheme)
	_ = k8upv1.AddToScheme(clientScheme)

	controllerClient, err := client.New(k8sConfig, client.Options{Scheme: clientScheme})
	if err != nil {
		return &RestoreTask{}, fmt.Errorf("failed to create client: %w", err)
	}
	namespaceClient := client.NewNamespacedClient(controllerClient, namespace)

	clientWithWatch, err := client.NewWithWatch(k8sConfig, client.Options{Scheme: clientScheme})
	if err != nil {
		return &RestoreTask{}, fmt.Errorf("failed to create watching client: %w", err)
	}

	clientSet, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return &RestoreTask{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	if taskId == "" {
		taskId = fmt.Sprintf("rnd-%04d", rand.IntN(9999))
	}

	return &RestoreTask{
		Args: TaskArgs{
			BackupId:      backupId,
			RestoreFilter: restoreFilter,
		},
		Client:         namespaceClient,
		WatchingClient: clientWithWatch,
		Clientset:      *clientSet,
		TaskId:         taskId,
		TaskKey:        fmt.Sprintf("rft-%s", taskId),
		TokenHost:      tokenHost,
		TokenPort:      tokenPort,
		APIHost:        apiHost,
		Ctx:            context.TODO(),
	}, nil
}

// CreateRestorePVC creates a PVC to attach to a k8up Restore.
func (t *RestoreTask) CreateRestorePVC() (corev1.PersistentVolumeClaim, error) {
	storageClassName := "bulk"
	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("target-%s", t.TaskKey),
			Annotations: map[string]string{
				"k8up.io/backup": "false", // Ensure backups skip this PVC.
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					// When bulk storage is backed by NFS, the size doesn't matter.
					// There is no way to know ahead of time how large the restored files will be.
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}

	err := t.Client.Create(t.Ctx, &pvc)
	if err != nil {
		return corev1.PersistentVolumeClaim{}, err
	}

	return pvc, nil
}

// StartRestore creates a k8up Restore resource to start restoring files from a backup.
func (t *RestoreTask) StartRestore(pvc corev1.PersistentVolumeClaim) (k8upv1.Restore, error) {
	// Load the Schedule resource to get restic config.
	var schedule k8upv1.Schedule
	if err := t.Client.Get(t.Ctx, client.ObjectKey{
		Name: "k8up-lagoon-backup-schedule",
	}, &schedule); err != nil {
		return k8upv1.Restore{}, fmt.Errorf("failed to get schedule: %w", err)
	}

	failedJobsHistoryLimit := 1
	newRestore := k8upv1.Restore{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.TaskKey,
		},
		Spec: k8upv1.RestoreSpec{
			Snapshot:      t.Args.BackupId,
			RestoreFilter: t.Args.RestoreFilter,
			RestoreMethod: &k8upv1.RestoreMethod{
				Folder: &k8upv1.FolderRestore{
					PersistentVolumeClaimVolumeSource: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvc.Name,
					},
				},
			},
			RunnableSpec: k8upv1.RunnableSpec{
				Backend: schedule.Spec.Backend,
			},
			KeepJobs:               &failedJobsHistoryLimit,
			FailedJobsHistoryLimit: &failedJobsHistoryLimit,
		},
	}

	err := t.Client.Create(t.Ctx, &newRestore)
	if err != nil {
		return k8upv1.Restore{}, fmt.Errorf("failed to create restore: %w", err)
	}

	return newRestore, nil
}

// WaitForRestore waits for the Restore to complete or timeout.
func (t *RestoreTask) WaitForRestore(restore k8upv1.Restore) error {
	w, err := t.WatchingClient.Watch(t.Ctx, &k8upv1.RestoreList{}, &client.ListOptions{
		Namespace:     restore.Namespace,
		FieldSelector: fields.OneTermEqualSelector("metadata.name", restore.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to watch restore: %w", err)
	}
	defer w.Stop()

	for event := range w.ResultChan() {
		restoreWatch, ok := event.Object.(*k8upv1.Restore)
		if !ok {
			// Watch query returned a non-restore type.
			continue
		}

		ready := meta.FindStatusCondition(restoreWatch.Status.Conditions, "Ready")
		if ready != nil {
			log.Printf("Restore progress: %s\n", ready.Message)
			if ready.Reason == "CreationFailed" {
				break
			}
		}

		progressing := meta.FindStatusCondition(restoreWatch.Status.Conditions, "Progressing")
		if progressing != nil && progressing.Status == metav1.ConditionTrue {
			log.Printf("Restore progress: %s\n", progressing.Message)
		}

		completed := meta.FindStatusCondition(restoreWatch.Status.Conditions, "Completed")
		if completed != nil && completed.Status == metav1.ConditionTrue {
			break
		}
	}

	w.Stop()

	return nil
}

// PrintRestoreLogs prints logs of pods that ran the restore to stdout.
// WARNING: Restore logs expose the backup webhook URL.
func (t *RestoreTask) PrintRestoreLogs(restore k8upv1.Restore) error {
	podList, err := t.Clientset.CoreV1().Pods(restore.Namespace).List(t.Ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("batch.kubernetes.io/job-name=restore-%s", restore.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to list restore pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return fmt.Errorf("failed to find restore pods")
	}

	for _, pod := range podList.Items {
		req := t.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		stream, err := req.Stream(t.Ctx)
		if err != nil {
			log.Printf("Failed to get logs: %v", err)
			continue
		}
		defer stream.Close()

		if _, err := io.Copy(log.Writer(), stream); err != nil {
			log.Printf("Failed to print logs: %v", err)
		}
	}

	return nil
}

// Cleanup cleans up PVC and Restore resources.
func (t *RestoreTask) Cleanup(pvc *corev1.PersistentVolumeClaim, restore *k8upv1.Restore) {
	if restore != nil {
		err := t.Client.Delete(t.Ctx, restore)
		if err != nil {
			log.Printf("Failed to clean up restore: %v", err)
		}
	}

	if pvc != nil {
		err := t.Client.Delete(t.Ctx, pvc)
		if err != nil {
			log.Printf("Failed to clean up pvc: %v", err)
		}
	}
}

// ArchiveRestore archives and compresses the restored files.
func (t *RestoreTask) ArchiveRestore(restoreTarget string, archiveTarget string) (*os.File, error) {
	_, err := os.Stat(restoreTarget)
	if err != nil {
		return &os.File{}, fmt.Errorf("invaid restore target %s: %v", restoreTarget, err)
	}

	// Specifying the files format as `"{restoreTarget}/": ""` ensures that the restore target dir is
	// excluded from the archive.
	rTarget := filepath.Clean(restoreTarget) + "/"
	files, err := archives.FilesFromDisk(t.Ctx, nil, map[string]string{
		rTarget: "",
	})
	if err != nil {
		return &os.File{}, fmt.Errorf("failed to parse restore target files: %v", err)
	}

	aTarget := filepath.Join(archiveTarget, fmt.Sprintf("restore-%s-t%s.tar.gz", t.Args.BackupId, t.TaskId))
	archive, err := os.Create(aTarget)
	if err != nil {
		return &os.File{}, fmt.Errorf("failed to create archive: %v", err)
	}
	defer archive.Close()

	format := archives.CompressedArchive{
		Compression: archives.Gz{},
		Archival:    archives.Tar{},
	}

	// Archive and compress the restored files.
	err = format.Archive(t.Ctx, archive, files)
	if err != nil {
		return &os.File{}, fmt.Errorf("failed to archive restore: %v", err)
	}

	return archive, nil
}

// UploadArchiveToLagoon uploads a given file to the Lagoon API.
func (t *RestoreTask) UploadArchiveToLagoon(archive *os.File) error {
	tkn, err := sshtoken.RetrieveToken("", t.TokenHost, t.TokenPort, nil, nil, false)
	if err != nil {
		return fmt.Errorf("failed to get Lagoon token: %v", err)
	}
	token := strings.TrimSpace(tkn)

	taskId, _ := strconv.Atoi(t.TaskId)
	lc := lclient.New(
		t.APIHost+"/graphql",
		fmt.Sprintf("RestoreTask-%s", TaskVersion),
		"0.x",
		&token,
		true)
	_, err = lagoon.UploadFilesForTask(context.TODO(), taskId, []string{archive.Name()}, lc)
	if err != nil {
		return fmt.Errorf("failed to upload restore to Lagoon task: %v", err)
	}

	return nil
}
