# See README.md: this image exists only to run kind inside another
# container. On a normal host, use kindest/node directly.
ARG BASE=kindest/node:v1.29.14
FROM ${BASE}
RUN mv /usr/local/sbin/runc /usr/local/sbin/runc.real
COPY runc /usr/local/sbin/runc
RUN chmod 0755 /usr/local/sbin/runc && /usr/local/sbin/runc --version
