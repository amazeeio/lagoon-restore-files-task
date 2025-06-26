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
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/amazeeio/lagoon-restore-files-task/internal/task"
	"k8s.io/client-go/tools/clientcmd"
)

func Execute() {
	// Load advanced task arguments from JSON_PAYLOAD env var.
	var backupIdArg, restoreFilterArg string
	if jsonPayloadEnc := os.Getenv("JSON_PAYLOAD"); jsonPayloadEnc != "" {
		jsonPayload, err := base64.StdEncoding.DecodeString(jsonPayloadEnc)
		if err == nil {
			var taskArgs task.TaskArgs
			err := json.Unmarshal(jsonPayload, &taskArgs)
			if err == nil {
				backupIdArg = taskArgs.BackupId
				restoreFilterArg = taskArgs.RestoreFilter
			}
		}
	}
	taskNamespaceEnv := os.Getenv("NAMESPACE")
	taskIdEnv := os.Getenv("TASK_DATA_ID")
	tokenHostEnv := os.Getenv("LAGOON_CONFIG_TOKEN_HOST")
	if tokenHostEnv == "" {
		tokenHostEnv = os.Getenv("TASK_SSH_HOST")
	}
	tokenPortEnv := os.Getenv("LAGOON_CONFIG_TOKEN_PORT")
	if tokenPortEnv == "" {
		tokenPortEnv = os.Getenv("TASK_SSH_PORT")
	}
	apiHostEnv := os.Getenv("LAGOON_CONFIG_API_HOST")
	if apiHostEnv == "" {
		apiHostEnv = os.Getenv("TASK_API_HOST")
	}

	// CLI flags for local development.
	kubeconfig := flag.String("kubeconfig", "", "Absolute path to a kubeconfig file")
	taskNamespace := flag.String("ns", taskNamespaceEnv, "Environment namespace")
	taskId := flag.String("tid", taskIdEnv, "Task ID")
	backupId := flag.String("bid", backupIdArg, "Backup ID")
	restoreFilter := flag.String("filter", restoreFilterArg, "Restore filter")
	restoreTarget := flag.String("restore-target", "/restore", "Path to restored files")
	archiveTarget := flag.String("archive-target", "/archive", "Path to archive of restored files")
	tokenHost := flag.String("token-host", tokenHostEnv, "SSH token host")
	tokenPort := flag.String("token-port", tokenPortEnv, "SSH token port")
	apiHost := flag.String("api-host", apiHostEnv, "Lagoon API host")
	taskImage := flag.String("task-image", "", "Task image")
	skipBootstrap := flag.Bool("skip-bootstrap", false, "Skip bootstrap upload pod")

	flag.Parse()

	if len(flag.Args()) < 1 {
		fmt.Println("Usage: restore-task [flags] [restore|upload]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Generate k8s config from file, fall back to in-cluster config.
	kConfig, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Fatalf("Failed to load kubernetes config: %v", err)
	}

	t, err := task.NewRestoreTask(
		*backupId,
		*restoreFilter,
		kConfig,
		*taskNamespace,
		*taskId,
		*tokenHost,
		*tokenPort,
		*apiHost,
	)
	if err != nil {
		log.Fatalf("Failed to load task config: %v", err)
	}

	subcommand := flag.Args()[0]

	// This is running as a sub-pod of the main task to upload the restored files.
	if subcommand == "upload" {
		if *backupId == "" || *taskId == "" || *tokenHost == "" || *tokenPort == "" || *apiHost == "" {
			log.Fatalf("Missing one of: backup id, task id, token host, token port, api host")
		}

		UploadPVCToTask(t, *restoreTarget, *archiveTarget)
		return
	}

	if subcommand != "restore" {
		log.Fatalf("Unknown subcommand %s", subcommand)
	}

	// This is the main task that restores files and starts a sub-pod to upload it to Lagoon.
	if *backupId == "" || *restoreFilter == "" || *taskNamespace == "" || *taskId == "" {
		log.Fatalf("Missing one of: namespace, task id, snapshot id, or restore filter")
	}

	log.Println("==================")
	log.Println("Restore Files Task")
	log.Printf("%s (%s — %s)", task.TaskVersion, task.BuildDate, task.GoVersion)
	log.Println("==================")
	fmt.Println()

	restoreResult, err := RestoreToPVC(t)
	if err != nil {
		log.Fatalf("Failed to restore backup: %v", err)
	}

	log.Println("Restore completed")

	if !*skipBootstrap {
		log.Println("Starting upload")
		fmt.Println()

		bootstrapResult, err := BootstrapUploadPod(t, *taskImage, *restoreTarget, restoreResult.PVC, *archiveTarget)
		if err != nil {
			restoreResult.Cleanup()
			log.Fatalf("Failed to upload restore to task: %v", err)
		}

		fmt.Println()
		log.Println("Upload completed")

		bootstrapResult.Cleanup()
	}

	restoreResult.Cleanup()

	fmt.Println()
	log.Println("==================")
	log.Println("Task completed")
	log.Println("==================")
}
