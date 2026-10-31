ARG DIND_IMAGE=docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac
FROM ${DIND_IMAGE}
RUN apk add --no-cache openssh-server \
    && mkdir -p /run/sshd /root/.ssh \
    && chmod 0700 /root/.ssh
COPY scripts/bootstrap-fixture/entrypoint.sh /usr/local/bin/bootstrap-fixture
ENTRYPOINT ["sh", "/usr/local/bin/bootstrap-fixture"]
