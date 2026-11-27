FROM ubuntu:20.04@sha256:8feb4d8ca5354def3d8fce243717141ce31e2c428701f6682bd2fafe15388214

COPY run-automdmd.sh /
RUN chmod +x /run-automdmd.sh

RUN apt-get update && apt-get install -y ca-certificates

RUN --mount=type=bind,source=automdm-osquery_amd64.deb,target=/tmp/automdm-osquery_amd64.deb \
    dpkg --install /tmp/automdm-osquery_amd64.deb

ENTRYPOINT ["/run-automdmd.sh"]
