FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go VERSION ./
ARG TARGETOS=linux
ARG TARGETARCH
ARG VCS_REF=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath -buildvcs=false -tags netgo,osusergo \
    -ldflags="-s -w -X main.revision=${VCS_REF}" -o /out/whoami .

FROM scratch
COPY --from=build /out/whoami /whoami
USER 65532:65532
ENV PORT=8080
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/whoami"]
