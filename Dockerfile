FROM alpine:latest

RUN apk add --no-cache openssh ipmitool util-linux && \
    ssh-keygen -A && \
    mkdir -p /root/.ssh && \
    chmod 700 /root/.ssh && \
    echo "PermitRootLogin yes" >> /etc/ssh/sshd_config && \
    echo "StrictModes no" >> /etc/ssh/sshd_config && \
    mkdir -p /etc/console-server /data/logs && \
    echo "nameserver 192.168.1.51" > /etc/resolv.conf

COPY console-server /usr/local/bin/console-server
COPY sol_helper.sh /usr/local/bin/sol_helper.sh
COPY entrypoint.sh /entrypoint.sh
COPY config.yaml.example /etc/console-server/config.yaml
RUN chmod +x /entrypoint.sh /usr/local/bin/console-server /usr/local/bin/sol_helper.sh

EXPOSE 22 80

ENTRYPOINT ["/entrypoint.sh"]
