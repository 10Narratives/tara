FROM gcr.io/distroless/base-debian12:nonroot

COPY build/faas-gateway /faas-gateway

EXPOSE 8080

ENTRYPOINT ["/faas-gateway"]
CMD ["--config", "/etc/faas/config.yaml", "--env", "dev"]