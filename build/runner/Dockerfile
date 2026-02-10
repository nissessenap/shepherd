FROM golang:1.25-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /shepherd-runner ./cmd/shepherd-runner/

FROM golang:1.25-bookworm

# Install tools (as root)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    bash \
    ca-certificates \
    make \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Install gh CLI (as root)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*

# Install Claude Code (native Bun binary, as root)
RUN curl -fsSL https://claude.ai/install.sh | bash

# Copy runner binary (root ownership is fine for system binaries)
COPY --from=builder /shepherd-runner /usr/local/bin/shepherd-runner

# Stage CC config files at /etc/shepherd/ (read-only in image).
# The Go entrypoint copies these to ~/.claude/ at startup because the
# emptyDir volume mount at /home/shepherd shadows any baked-in files.
COPY build/runner/CLAUDE.md /etc/shepherd/CLAUDE.md
COPY build/runner/settings.json /etc/shepherd/settings.json

# Create non-root user (after all root-requiring operations)
RUN useradd -m -u 1000 -s /bin/bash shepherd

# Create workspace directory (owned by shepherd)
RUN mkdir -p /workspace && chown shepherd:shepherd /workspace

USER shepherd
WORKDIR /home/shepherd

# Configure git identity (as shepherd user)
RUN git config --global user.name "Shepherd Bot" \
    && git config --global user.email "shepherd-bot@users.noreply.github.com"

EXPOSE 8888
ENTRYPOINT ["shepherd-runner"]
