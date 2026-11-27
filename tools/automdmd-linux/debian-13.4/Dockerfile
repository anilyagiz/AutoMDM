FROM debian:13.4@sha256:e2d08da6f42ef4b09b165d55528a12727aeed8240dc9edf888e3ec07e10ef9da

COPY run-automdmd.sh /
RUN chmod +x /run-automdmd.sh

RUN apt-get update && apt-get install -y ca-certificates

RUN --mount=type=bind,source=automdm-osquery_amd64.deb,target=/tmp/automdm-osquery_amd64.deb \
    dpkg --install /tmp/automdm-osquery_amd64.deb

ENTRYPOINT ["/run-automdmd.sh"]
