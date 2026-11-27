# Using `toggle-mdm-dev` to enable and disable MDM (Mobile Device Management) / AB (Apple Business) for development

1. Set up all of the necessary credentials for using MDM and AB as outlined in the [MDM setup and
   testing
   docs](https://automdm.com/docs/contributing/testing-and-local-development#mdm-setup-and-testing).
   Take note of the path where you've stored these credentials.

2. Make an automdm env file containing the following or similar logic, including the above path where specified:

  ```
    if [[ $USE_MDM == "1" ]]; then

    # MDM Feature Flag:

    MDM_PATH="{Replace this string, including braces, with the path to your credentials}"

    # Apple Push Certificates
    export AUTOMDM_MDM_APPLE_SCEP_CHALLENGE=scepchallenge
    export AUTOMDM_MDM_APPLE_SCEP_CERT=$MDM_PATH"automdm-mdm-apple-scep.crt"
    export AUTOMDM_MDM_APPLE_SCEP_KEY=$MDM_PATH"automdm-mdm-apple-scep.key"

    # Apple Push Notification Service (APNS) credentials
    export AUTOMDM_MDM_APPLE_APNS_CERT=$MDM_PATH"mdmcert.download.push.pem"
    export AUTOMDM_MDM_APPLE_APNS_KEY=$MDM_PATH"mdmcert.download.push.key"

    # Apple Business (AB) credentials
    export AUTOMDM_MDM_APPLE_BM_SERVER_TOKEN=$MDM_PATH"downloadtoken.p7m"
    export AUTOMDM_MDM_APPLE_BM_CERT=$MDM_PATH"automdm-apple-mdm-bm-public-key.crt"
    export AUTOMDM_MDM_APPLE_BM_KEY=$MDM_PATH"automdm-apple-mdm-bm-private.key"

    else

    unset AUTOMDM_MDM_APPLE_SCEP_CHALLENGE
    unset AUTOMDM_MDM_APPLE_SCEP_CERT
    unset AUTOMDM_MDM_APPLE_SCEP_KEY
    unset AUTOMDM_MDM_APPLE_BM_SERVER_TOKEN
    unset AUTOMDM_MDM_APPLE_BM_CERT
    unset AUTOMDM_MDM_APPLE_BM_KEY
    #below files are from the shared AutoMDM 1Password
    unset AUTOMDM_MDM_APPLE_APNS_CERT
    unset AUTOMDM_MDM_APPLE_APNS_KEY
    fi
  ```

3. If you haven't already, add an environment variable called `AUTOMDM_ENV_PATH` to your shell config
   file. Source it or open a new shell.
4. Add the directory containing `toggle-mdm-dev`, likely this one, to your $PATH. If you did that by
   adding it to your shell config, source it or open a new shell.
5. To toggle MDM and AB, execute `source toggle-mdm-dev`
6. To enable MDM without AB set up, comment out the variables in your env file pointing to the
   various credentials (like below), then `source toggle-mdm-dev` *twice*, to toggle off then back on again, the
   MDM feature flag.
  
  ```
  if [[ $USE_MDM == "1" ]]; then

  # MDM_PATH="/Users/jacob/.envs/automdm_env/mdm/"

  # # Apple Push Certificates
  # export AUTOMDM_MDM_APPLE_SCEP_CHALLENGE=scepchallenge
  # export AUTOMDM_MDM_APPLE_SCEP_CERT=$MDM_PATH"automdm-mdm-apple-scep.crt"
  # export AUTOMDM_MDM_APPLE_SCEP_KEY=$MDM_PATH"automdm-mdm-apple-scep.key"

  # # APNS credentials from AutoMDM shared 1Password
  # export AUTOMDM_MDM_APPLE_APNS_CERT=$MDM_PATH"mdmcert.download.push.pem"
  # export AUTOMDM_MDM_APPLE_APNS_KEY=$MDM_PATH"mdmcert.download.push.key"

  # # Apple Business
  # export AUTOMDM_MDM_APPLE_BM_SERVER_TOKEN=$MDM_PATH"downloadtoken.p7m"
  # export AUTOMDM_MDM_APPLE_BM_CERT=$MDM_PATH"automdm-apple-mdm-bm-public-key.crt"
  # export AUTOMDM_MDM_APPLE_BM_KEY=$MDM_PATH"automdm-apple-mdm-bm-private.key"
  ```
