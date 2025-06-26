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
	"errors"
	"fmt"
	"log"

	"github.com/amazeeio/lagoon-restore-files-task/internal/task"
	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RestoreToPVCResult struct {
	PVC     *corev1.PersistentVolumeClaim
	Restore *k8upv1.Restore
	Cleanup func()
}

// RestoreToPVC creates a PVC and restores a backup to it.
func RestoreToPVC(t *task.RestoreTask) (*RestoreToPVCResult, error) {
	log.Printf("Restoring %s from backup %s", t.Args.RestoreFilter, t.Args.BackupId)

	log.Printf("Restore task name: %s", t.TaskKey)
	fmt.Println()

	pvc, err := t.CreateRestorePVC(fmt.Sprintf("restore-target-%s", t.TaskKey), "1Gi")
	if err != nil {
		log.Fatalf("Failed to create restore destination: %v", err)
	}

	restore, err := t.StartRestore(pvc)
	if err != nil {
		t.Cleanup(&pvc, nil, nil)
		log.Fatalf("Failed to start restore: %v", err)
	} else {
		log.Println("Starting restore")
	}

	err = t.WaitForRestore(restore)
	if err != nil {
		t.Cleanup(&pvc, &restore, nil)
		log.Fatalf("Failed to wait for restore: %v", err)
	}
	fmt.Println()

	// Determine if the restore was a succcess.
	var restoreFailed error
	if err := t.Client.Get(t.Ctx, client.ObjectKey{Name: restore.Name}, &restore); err != nil {
		restoreFailed = fmt.Errorf("failed to get restore: %w", err)
	} else {
		restoreCompleted := meta.FindStatusCondition(restore.Status.Conditions, "Completed")

		if restoreCompleted == nil { // Triggered with condition Ready: CreationFailed.
			restoreFailed = fmt.Errorf("restore status: %+v", restore.Status)
		} else if restoreCompleted.Reason == "Failed" {
			restoreFailed = errors.New(restoreCompleted.Message)
		}
	}

	if restoreFailed != nil {
		// // Manually created restores don't honor the FailedJobsHistoryLimit setting.
		// // Attempting to gather logs anyway is a hail mary.
		// log.Println("====== Restore logs ======")
		// err := rt.PrintRestoreLogs(restore)
		// if err != nil {
		// 	log.Printf("Failed to get logs: %v", err)
		// }

		t.Cleanup(&pvc, &restore, nil)

		return &RestoreToPVCResult{}, fmt.Errorf("restore failed: %w", restoreFailed)
	} else {
		// log.Println("====== Restore logs ======")
		// err := rt.PrintRestoreLogs(restore)
		// if err != nil {
		// 	log.Printf("Failed to get logs: %v", err)
		// }

		return &RestoreToPVCResult{
			PVC:     &pvc,
			Restore: &restore,
			Cleanup: func() { t.Cleanup(&pvc, &restore, nil) },
		}, nil
	}
}
