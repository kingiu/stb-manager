FROM alpine:3.19
RUN apk add --no-cache dumb-init
COPY stb-manager /usr/local/bin/stb-manager
RUN chmod +x /usr/local/bin/stb-manager
RUN mkdir -p /opt/ota
WORKDIR /opt/ota
EXPOSE 8080
CMD ["dumb-init", "/usr/local/bin/stb-manager"]
