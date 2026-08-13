# Harness image for the api and ui projects. They run as a container on
# step-network, not on the host: both rate limiters key on the client IP, so a
# host harness is one gateway address and per-test rate-limit isolation becomes
# impossible (Section 4.1.2).
#
# The tag must match @playwright/test in package.json, or the reporter and the
# runtime disagree. The digest pins the browser build; an unpinned image changes
# browser version between runs.
ARG PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.62.1-noble@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e
# hadolint ignore=DL3006
FROM ${PLAYWRIGHT_IMAGE}

# docker-ce-cli and the compose plugin are the single mechanism every
# container- or database-touching assertion uses over the mounted socket.
# hadolint ignore=DL3008,DL3015
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gnupg openssl postgresql-client \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
 && chmod a+r /etc/apt/keyrings/docker.asc \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" \
      > /etc/apt/sources.list.d/docker.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
 && apt-get purge -y gnupg \
 && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /e2e

# node_modules is baked into its own layer, so no job pays a cold npm ci.
COPY package.json package-lock.json ./
RUN npm ci

COPY . .

ENTRYPOINT ["npx", "playwright", "test"]
