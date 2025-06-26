# Restore Files Image Task

This repo can be used to add a Lagoon Advanced Task to a project which can restore individual
directories or files from an environment backup.

## How it works

A Lagoon advanced task is added to a project which uses the image created from this repo. The task
triggers the restore of a specific path for a given k8up backup. After the restore is complete, the
files are compressed and uploaded as a file to the advanced task.

1. Create a PVC to act as the restore target.
2. Create a k8up retore with the restore target PVC.
3. Wait for the restore to complete.
4. Create a new pod with the restore target PVC.
5. Compress files in the restore target and upload to Lagoon API.
6. Clean up all resources.

## Local development

1. Start a Lagoon local stack with [k8up installed](https://github.com/uselagoon/lagoon/blob/main/Makefile#L495).
2. Get a kubeconfig `./local-dev/k3d kubeconfig write lagoon`.
3. Deploy an environment of one of the test projects.

   1. Install the Drupal site `drush si -y`.
   2. Upload some media files.

4. Edit the k8up Schedule resource to take a backup more frequently (eg `*/5 * * * *`).
5. Pick a backup ID that backed up `nginx`.

### Testing restore

1. Run this command `go run . -kubeconfig ~/.config/k3d/kubeconfig-lagoon.yaml -bid 6c91b29 -filter /data/nginx/css -ns lagoon-demo-org-main -tid 0 restore`.
2. Monitor the relevant k8s resources in the provided namespace: k8upv1.Restore, batchv1.Job, corev1.Pod, corev1.PersistentVolumeClaim.

### Testing upload

1. Create some dummy local files to upload, eg `./restore-target/dummy.txt`, and an archive path, eg `./archive-target`.
2. Ensure you have an ssh-agent running with a key added to your k3d lagoon.
3. Run any task from the UI for the deployed environment from previous steps. Note the task ID.
4. Run this command `go run . -kubeconfig ~/.config/k3d/kubeconfig-lagoon.yaml -tid 127 -token-host lagoon-ssh.172.20.0.242.nip.io -token-port 2020 -api-host 'http://lagoon-api.172.20.0.240.nip.io' -restore-target restore-target -archive-target archive-target upload`'
5. Reload the task page and check the archive was uploaded.
