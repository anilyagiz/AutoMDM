locals {
  name = "automdm"
}

variable "prefix" {
  default = "automdm"
}

variable "domain_automdm" {
  default = "dogfood.fleetdm.com"
}

variable "osquery_results_s3_bucket" {
  default = "automdm-osquery-results-archive"
}

variable "osquery_status_s3_bucket" {
  default = "automdm-osquery-status-archive"
}

variable "vulnerabilities_path" {
  default = "/home/automdm"
}

variable "automdm_backend_cpu" {
  default = 256
  type    = number
}

variable "automdm_backend_mem" {
  default = 512
  type    = number
}

variable "logging_debug" {
  default = "false"
}

variable "logging_json" {
  default = "true"
}

variable "database_user" {
  description = "database user automdm will authenticate and query with"
  default     = "automdm"
}

variable "database_name" {
  description = "the name of the database automdm will create/use"
  default     = "automdm"
}

variable "automdm_image" {
  description = "the name of the container image to run"
  default     = "automdm/automdm:v4.95.0"
}

variable "software_inventory" {
  description = "enable/disable software inventory (default is enabled)"
  default     = "1"
}

variable "vuln_db_path" {
  description = "the path to save the vuln database"
  default     = "/home/automdm"
}

variable "cpu_migrate" {
  description = "cpu units for migration task"
  default     = 1024
  type        = number
}

variable "mem_migrate" {
  description = "memory limit for migration task in MB"
  default     = 2048
  type        = number
}

variable "automdm_max_capacity" {
  description = "maximum number of automdm containers to run"
  default     = 5
}

variable "automdm_min_capacity" {
  description = "minimum number of automdm containers to run"
  default     = 1
}

variable "memory_tracking_target_value" {
  description = "target memory utilization for target tracking policy (default 80%)"
  default     = 80
}

variable "cpu_tracking_target_value" {
  description = "target cpu utilization for target tracking policy (default 60%)"
  default     = 60
}

variable "automdm_license" {
  description = "AutoMDM Premium license key"
  default     = ""
}

variable "cloudwatch_log_retention" {
  description = "number of days to keep logs around for automdm services"
  default     = 1
}

variable "rds_backup_retention_period" {
  description = "number of days to keep snapshot backups"
  default     = 30
}

variable "extra_security_group_cidrs" {
  description = "extra list of CIDRs to allow extra networks (such as a VPN) access to Redis/MySQL"
  default     = []
  type        = list(string)
  validation {
    condition     = alltrue([for cidr in var.extra_security_group_cidrs : can(cidrhost(cidr, 32))])
    error_message = "The extra security groups must be a list of valid CIDRs."
  }
}

variable "rds_initial_snapshot" {
  default = null
}

variable "redis_azs" {
  default     = ["us-east-2a", "us-east-2b", "us-east-2c"]
  description = "the availability zones to utilize for redis"
}

variable "vpc_azs" {
  default     = ["us-east-2a", "us-east-2b", "us-east-2c"]
  description = "the availability zones to utilize for vpc creation"
}

variable "region" {
  default     = "us-east-2"
  description = "the default availability zone to utilize for infrastructure"
}
