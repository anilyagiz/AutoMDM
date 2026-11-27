output "private_subnets" {
  value = data.terraform_remote_state.shared.outputs.vpc.private_subnet_arns
}

output "automdm_migration_revision" {
  value = aws_ecs_task_definition.migration.revision
}

output "automdm_migration_subnets" {
  value = jsonencode(aws_ecs_service.automdm.network_configuration[0].subnets)
}

output "automdm_migration_security_groups" {
  value = jsonencode(aws_ecs_service.automdm.network_configuration[0].security_groups)
}

output "automdm_ecs_cluster_arn" {
  value = aws_ecs_cluster.automdm.arn
}

output "automdm_ecs_cluster_id" {
  value = aws_ecs_cluster.automdm.id
}
