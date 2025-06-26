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
	"log"
	"os"

	"github.com/amazeeio/lagoon-restore-files-task/internal/task"
)

// UploadPVCToTask compresses the restored files in the PVC and uploads it to the Lagoon task.
func UploadPVCToTask(t *task.RestoreTask, restoreTarget string, archiveTarget string) {
	log.Println("Compressing restored files")

	archive, err := t.ArchiveRestore(restoreTarget, archiveTarget)
	if err != nil {
		// Cleanup is handled by parent task process.
		log.Fatalf("Failed to archive restored files: %v", err)
	}

	log.Printf("Uploading restore archive to Lagoon task %s", t.TaskId)

	err = t.UploadArchiveToLagoon(archive)
	if err != nil {
		log.Fatalf("Failed to upload: %v", err)
	}

	os.Exit(0)
}
