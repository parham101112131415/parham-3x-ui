# ========================================================
# Stage: Frontend (Vite)
# ========================================================
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
COPY internal/web/translation /src/internal/web/translation
RUN npm run build

# ========================================================
# Stage: Builder
# ========================================================
FROM golang:1.27-alpine AS builder
WORKDIR /app
ARG TARGETARCH
ARG TARGETVARIANT

RUN apk --no-cache --update add \
  build-base \
  gcc \
  curl \
  unzip

COPY . .
COPY --from=frontend /src/internal/web/dist ./internal/web/dist

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-D_LARGEFILE64_SOURCE"
RUN go build -ldflags "-w -s" -o build/x-ui main.go
RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT"

# ========================================================
# Stage: Final Image of 3x-ui
# ========================================================
FROM alpine
ENV TZ=Asia/Tehran
WORKDIR /app

RUN apk add --no-cache --update \
  ca-certificates \
  tzdata \
  fail2ban \
  bash \
  curl \
  openssl \
  nginx \
  supervisor
COPY --from=builder /app/build/ /app/
COPY --from=builder /app/DockerEntrypoint.sh /app/
COPY --from=builder /app/x-ui.sh /usr/bin/x-ui
COPY --from=builder /app/internal/web/translation /app/internal/web/translation
# GeoLite2 City database (community mirror of MaxMind's free database):
# the panel resolves client-IP locations locally and shows the accuracy
# radius, so no user IP ever leaves the server for a geo lookup.
RUN mkdir -p /usr/local/share/geoip \
  && curl -fL -o /usr/local/share/geoip/GeoLite2-City.mmdb \
    "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download/GeoLite2-City.mmdb" \
  && test "$(stat -c%s /usr/local/share/geoip/GeoLite2-City.mmdb)" -gt 10000000
ENV PARHAM_GEOLITE2_CITY_DB="/usr/local/share/geoip/GeoLite2-City.mmdb"
# Adult-site domain list for the visited-sites 🔞 labels and the Telegram
# adult-site watcher: merged at build time from three maintained public
# blocklists (BlocklistProject porn, oisd nsfw, Steven Black porn-only)
# and normalized to one bare domain per line. A source being down only
# shrinks the merge; the build fails only if the union is implausibly
# small (< 500k domains).
RUN mkdir -p /usr/local/share/parham /tmp/adultlists \
  && (curl -sfL -o /tmp/adultlists/blp.txt "https://raw.githubusercontent.com/blocklistproject/Lists/master/porn.txt" || true) \
  && (curl -sfL -o /tmp/adultlists/sb.txt "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/porn-only/hosts" || true) \
  && (curl -sfL -o /tmp/adultlists/oisd.txt "https://nsfw.oisd.nl/domainswild" || true) \
  && cat /tmp/adultlists/*.txt | awk '{ gsub(/\r/, ""); if ($0 == "" || $0 ~ /^#/ || $0 ~ /^!/) next; f = $1; if (f == "0.0.0.0" || f == "127.0.0.1" || f == "::1") f = $2; sub(/^\*\./, "", f); f = tolower(f); sub(/\.$/, "", f); if (f ~ /^[a-z0-9.-]+$/ && f ~ /\./ && f !~ /^\./ && f !~ /\.\./) print f }' | sort -u > /usr/local/share/parham/adult_domains.txt \
  && test "$(wc -l < /usr/local/share/parham/adult_domains.txt)" -gt 500000 \
  && rm -rf /tmp/adultlists
ENV PARHAM_ADULT_SITES_FILE="/usr/local/share/parham/adult_domains.txt"

COPY nginx/default.conf /etc/nginx/http.d/default.conf
COPY nginx/nginx.conf /etc/nginx/nginx.conf
COPY nginx/hidden.conf /app/nginx-hidden.conf
COPY nginx/game.html /app/hidden/game.html
COPY supervisord.conf /etc/supervisord.conf


# Configure fail2ban
RUN rm -f /etc/fail2ban/jail.d/alpine-ssh.conf \
  && cp /etc/fail2ban/jail.conf /etc/fail2ban/jail.local \
  && sed -i "s/^\[ssh\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/^\[sshd\]$/&\nenabled = false/" /etc/fail2ban/jail.local \
  && sed -i "s/#allowipv6 = auto/allowipv6 = auto/g" /etc/fail2ban/fail2ban.conf

RUN chmod +x \
  /app/DockerEntrypoint.sh \
  /app/x-ui \
  /usr/bin/x-ui

RUN mkdir -p /tmp/nginx/client_body /tmp/nginx/proxy /tmp/nginx/fastcgi /tmp/nginx/uwsgi /tmp/nginx/scgi
ENV XUI_IN_DOCKER="true"
ENV XUI_MAIN_FOLDER="/app"
ENV XUI_ENABLE_FAIL2BAN="true"
# OMID public HTTPS proxy: Nginx fronts the panel and subscription server.
ENV XUI_PORT="8080"
ENV XUI_SKIP_HSTS="true"
ENV XUI_SUB_PUBLIC_HTTPS="true"
ENV XUI_DB_TYPE=""
ENV XUI_DB_DSN=""
EXPOSE 2053
VOLUME [ "/etc/x-ui" ]
CMD [ "./x-ui" ]
ENTRYPOINT [ "/app/DockerEntrypoint.sh" ]
