FROM alpine:3.23.8@sha256:5c216c9767869f6ec7c4718977601d52f087c5c7bf2fdfb198e403f6b48ec40d
ARG AUTOMDM_VERSION=unknown
LABEL maintainer="AutoMDM Developers"
LABEL org.opencontainers.image.version="${AUTOMDM_VERSION}"
LABEL org.opencontainers.image.vendor="Fleet Device Management Inc."
LABEL org.opencontainers.image.title="automdm"
LABEL org.opencontainers.image.source="https://github.com/anilyagiz/automdm"
LABEL org.opencontainers.image.url="https://automdm.com"
LABEL org.opencontainers.image.documentation="https://automdm.com/docs"

RUN apk --update add ca-certificates
RUN apk --no-cache upgrade openssl libcrypto3 libssl3

# Create team group and user
RUN addgroup -S automdm && adduser -S automdm -G automdm

USER automdm

COPY automdm /usr/bin/

CMD ["automdm", "serve"]
