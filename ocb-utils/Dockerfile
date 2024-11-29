FROM alpine:latest AS prep
RUN apk --update add ca-certificates

FROM public.ecr.aws/docker/library/golang:1.23.2 AS build

WORKDIR /tmp/build/
RUN go env -w GOPROXY=direct
RUN curl --proto '=https' --tlsv1.2 -fL -o ocb https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv${OCB_VERSION}/ocb_${OCB_VERSION}_linux_amd64

RUN chmod +x ocb


COPY builder-config-appsignals.yaml .

RUN --mount=type=cache,target=/go/pkg/mod ./ocb --config builder-config-appsignals.yaml

FROM public.ecr.aws/amazonlinux/amazonlinux:minimal

ARG USER_UID=10001
USER ${USER_UID}

COPY --from=prep /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /tmp/build/otelcol-appsignals/otelcol-appsignals /

WORKDIR /

EXPOSE 4317 4318 13133 2000 4316 4315 55680 55679
ENTRYPOINT ["/otelcol-appsignals"]
CMD ["--config", "/etc/otel/config.yaml"]
