FROM mongo:8.3.17

RUN useradd --create-home --home-dir /home/container --shell /bin/bash container

COPY --chmod=0755 entrypoint.sh /usr/local/bin/manta-mongodb-entrypoint

ENV HOME=/home/container USER=container MONGODB_CONFIG_OVERRIDE_NOFORK=true
WORKDIR /home/container
USER container
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/manta-mongodb-entrypoint"]
CMD []
