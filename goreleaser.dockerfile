FROM scratch
WORKDIR /
COPY crd-bootstrap /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
