# Static frontend served by nginx as a non-root user (UID 101, port 8080).
FROM nginxinc/nginx-unprivileged:1.30.6-alpine3.24

# API_UPSTREAM: where /api/ is proxied to; overridden per environment (compose service name, Kubernetes FQDN).
# NGINX_ENTRYPOINT_LOCAL_RESOLVERS: export the container's DNS servers as NGINX_LOCAL_RESOLVERS for `resolver`.
# NGINX_ENVSUBST_FILTER: substitute only these variables in the template, never nginx's own $host, $uri...
ENV API_UPSTREAM=http://api:8080 \
    NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1 \
    NGINX_ENVSUBST_FILTER="^(API_UPSTREAM|NGINX_LOCAL_RESOLVERS)$"

COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY public/ /usr/share/nginx/html/

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/nginx-health || exit 1
