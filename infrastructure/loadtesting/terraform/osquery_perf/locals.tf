locals {
  customer            = "automdm-${terraform.workspace}"
  loadtest_containers = var.loadtest_containers

  # automdm_image  = var.tag
  loadtest_tag = var.git_tag_branch
}
