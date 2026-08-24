FROM scratch
ARG SERVICE
COPY bin/linux/${SERVICE} /service
USER 65532:65532
ENTRYPOINT ["/service"]
