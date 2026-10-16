ARG MANTA_BASE_IMAGE=manta:local-test

FROM busybox:1.37.0-musl AS probe

FROM ${MANTA_BASE_IMAGE}
COPY --from=probe /bin/busybox /usr/local/bin/busybox
