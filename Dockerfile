FROM scratch
COPY ipmiserial /ipmiserial
COPY config.yaml.example /config.yaml
EXPOSE 80
ENTRYPOINT ["/ipmiserial", "-config", "/config.yaml"]
