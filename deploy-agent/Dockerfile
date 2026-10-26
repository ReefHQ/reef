ARG DOCKER_IMAGE=docker:29.9.2-cli
FROM ${DOCKER_IMAGE}
RUN apk add --no-cache curl webhook
COPY deploy.sh /scripts/deploy.sh
COPY hooks.json /hooks/hooks.json
RUN chmod 755 /scripts/deploy.sh
WORKDIR /app
EXPOSE 9009
ENTRYPOINT ["/usr/bin/webhook"]
CMD ["-hooks", "/hooks/hooks.json", "-port", "9009"]
