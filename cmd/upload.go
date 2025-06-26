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

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/amazeeio/lagoon-restore-files-task/internal/task"
	"github.com/dustin/go-humanize"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UploadPVCToTask compresses the restored files in the PVC and uploads it to the Lagoon task.
func UploadPVCToTask(t *task.RestoreTask, restoreTarget string, archiveTarget string) {
	log.Println("Archiving restored files")

	archive, err := t.ArchiveRestore(restoreTarget, archiveTarget)
	if err != nil {
		// Cleanup is handled by parent task process.
		log.Fatalf("Failed to archive restored files: %v", err)
	}

	archiveInfo, err := os.Stat(archive.Name())
	if err != nil {
		log.Fatalf("Failed to read archive: %v", err)
	}

	log.Printf("Uploading %s (%s) to Lagoon task %s", archive.Name(), humanize.Bytes(uint64(archiveInfo.Size())), t.TaskId)

	err = t.UploadArchiveToLagoon(archive)
	if err != nil {
		log.Fatalf("Failed to upload: %v", err)
	}

	os.Exit(0)
}

type BootstrapResult struct {
	uploadPod *corev1.Pod
	Cleanup   func()
}

// BootstrapUploadPod creates a new pod with the restore PVC, a PVC to save the archived files, and
// runs the `upload` sub-subcommand.
func BootstrapUploadPod(t *task.RestoreTask, taskImage string, restoreTarget string, restorePVC *corev1.PersistentVolumeClaim, archiveTarget string) (*BootstrapResult, error) {
	uploadPodImageName := taskImage
	var self corev1.Pod
	if err := t.Client.Get(t.Ctx, client.ObjectKey{Name: os.Getenv("PODNAME")}, &self); err == nil {
		uploadPodImageName = self.Spec.Containers[0].Image
	}
	if uploadPodImageName == "" {
		return &BootstrapResult{}, fmt.Errorf("failed to determine task image")
	}

	jsonPayload, err := json.Marshal(t.Args)
	if err != nil {
		return &BootstrapResult{}, fmt.Errorf("failed to marshal task args: %w", err)
	}

	archivePVC, err := t.CreateRestorePVC(fmt.Sprintf("archive-target-%s", t.TaskKey), "1Gi")
	if err != nil {
		t.Cleanup(&archivePVC, nil, nil)
		return &BootstrapResult{}, fmt.Errorf("failed to create archive destination: %v", err)
	}

	var defaultMode int32 = 420
	var pod = corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("upload-%s", t.TaskKey),
			Annotations: map[string]string{
				"k8up.io/backup": "false", // Ensure backups skip this pod.
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "restore-target",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: restorePVC.Name,
						},
					},
				},
				{
					Name: "archive-target",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: archivePVC.Name,
						},
					},
				},
				{
					Name: "lagoon-sshkey",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  "lagoon-sshkey",
							DefaultMode: &defaultMode,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "uploader",
					Image:   uploadPodImageName,
					Command: []string{"/usr/local/bin/restore-files-task", "upload"},
					Env: []corev1.EnvVar{
						{
							Name:  "JSON_PAYLOAD",
							Value: base64.StdEncoding.EncodeToString(jsonPayload),
						},
						{
							Name:  "TASK_DATA_ID",
							Value: t.TaskId,
						},
						{
							Name:  "LAGOON_CONFIG_TOKEN_HOST",
							Value: t.TokenHost,
						},
						{
							Name:  "LAGOON_CONFIG_TOKEN_PORT",
							Value: t.TokenPort,
						},
						{
							Name:  "LAGOON_CONFIG_API_HOST",
							Value: t.APIHost,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "lagoon-sshkey",
							ReadOnly:  true,
							MountPath: "/var/run/secrets/lagoon/ssh",
						},
						{
							Name:      "restore-target",
							MountPath: restoreTarget,
						},
						{
							Name:      "archive-target",
							MountPath: archiveTarget,
						},
					},
				},
			},
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: "lagoon-deployer",
		},
	}

	err = t.Client.Create(context.TODO(), &pod)
	if err != nil {
		t.Cleanup(&archivePVC, nil, &pod)
		return &BootstrapResult{}, fmt.Errorf("failed to create upload pod: %v", err)
	}

	err = t.WaitForUpload(pod)
	if err != nil {
		t.Cleanup(&archivePVC, nil, &pod)
		return &BootstrapResult{}, fmt.Errorf("failed to wait for upload: %v", err)
	}

	// Determine if the upload was a succcess.
	var uploadFailed error
	if err := t.Client.Get(t.Ctx, client.ObjectKey{Name: pod.Name}, &pod); err != nil {
		uploadFailed = fmt.Errorf("failed to get upload pod: %w", err)
	} else {
		if pod.Status.Phase == corev1.PodFailed {
			uploadFailed = errors.New(pod.Status.Message)
		}
	}

	log.Println("====== Upload logs ======")
	err = t.PrintUploadLogs(pod)
	if err != nil {
		log.Printf("Failed to get logs: %v", err)
	}

	if uploadFailed != nil {
		t.Cleanup(&archivePVC, nil, &pod)
		return &BootstrapResult{}, fmt.Errorf("upload failed: %w", uploadFailed)
	} else {
		return &BootstrapResult{
			uploadPod: &pod,
			Cleanup: func() {
				t.Cleanup(&archivePVC, nil, &pod)
			},
		}, nil
	}
}
