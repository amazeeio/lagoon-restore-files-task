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
6. Run this program `go run . -kubeconfig ~/.config/k3d/kubeconfig-lagoon.yaml -bid 6c91b29 -filter /data/nginx/css -ns lagoon-demo-org-main -tid 0 restore`.
