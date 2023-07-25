FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY crd-bootstrap /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
