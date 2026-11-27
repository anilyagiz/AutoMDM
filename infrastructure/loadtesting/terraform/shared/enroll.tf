resource "aws_secretsmanager_secret" "enroll_secret" {
  name       = "/automdm/loadtest/enroll/${random_pet.main.id}"
  kms_key_id = aws_kms_key.main.id
}

# Google service account credentials for Android AMAPI mock forwarding.
resource "aws_secretsmanager_secret" "android_google_credentials" {
  name       = "/automdm/loadtest/android-google-credentials/${random_pet.main.id}"
  kms_key_id = aws_kms_key.main.id
}
