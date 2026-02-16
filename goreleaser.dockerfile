FROM gcr.io/distroless/static:nonroot
ARG TARGETPLATFORM
WORKDIR /
COPY ${TARGETPLATFORM}/crd-bootstrap /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
