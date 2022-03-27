FROM debian:11.2-slim

WORKDIR /app
COPY build/space .

EXPOSE 7664

CMD ["/app/space", "-f", "/usr/local/etc/space/space.toml"]

