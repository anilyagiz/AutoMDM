# desktop-rate-limit

Tool to brute force AutoMDM Desktop endpoints to test rate-limiting via IP banning.

Usage:
```sh
# -automdm_desktop_token can be set to a valid AutoMDM Desktop token to simulate good traffic alongside
# unauthenticated requests.
go run ./tools/desktop-rate-limit/main.go \
    -automdm_url https://localhost:8080 \
    -automdm_desktop_token 62381ddb-63a0-41da-bd2f-dd13a462cc6
```
