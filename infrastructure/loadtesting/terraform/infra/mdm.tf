resource "random_password" "challenge" {
  length  = 12
  special = false
}

resource "aws_secretsmanager_secret_version" "scep" {
  secret_id = module.mdm.scep.id
  secret_string = jsonencode(
    {
      AUTOMDM_MDM_APPLE_SCEP_CERT_BYTES = tls_self_signed_cert.scep_cert.cert_pem
      AUTOMDM_MDM_APPLE_SCEP_KEY_BYTES  = tls_private_key.scep_key.private_key_pem
      AUTOMDM_MDM_APPLE_SCEP_CHALLENGE  = random_password.challenge.result
    }
  )
}