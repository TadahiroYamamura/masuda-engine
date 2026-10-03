# masuda-engineをmasudaで開発するためのゲストイメージ。構成はmasudaリポジトリの
# .masuda/images/default/Dockerfileに倣う。`masuda image build default` または `masuda run` の
# 起動時に sandbox service が Docker でビルドし、VMのrootfsにする。
#
# go.mod/go.sum を変えたら ctx/ の写しも更新すること（依存をイメージに前取りするため。
# settings.json の egress は空なので、VMから proxy.golang.org には出られない）:
#   cp go.mod go.sum .masuda/images/default/ctx/
#
# Claude Code の版は、masuda の internal/guest/guest.go の ClaudeCodeVersion（masudaが実機で
# 検証した版）に合わせる。masuda 側で上がったらここも上げる。
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl git tmux openssh-server \
 && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://go.dev/dl/go1.26.8.linux-amd64.tar.gz | tar -C /usr/local -xz \
 && ln -s /usr/local/go/bin/go /usr/local/bin/go \
 && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

RUN mkdir -p /workspace /masuda && chown ubuntu:ubuntu /workspace /masuda
COPY --chown=ubuntu:ubuntu ctx/go.mod ctx/go.sum /tmp/modprefetch/
USER ubuntu
WORKDIR /home/ubuntu
RUN curl -fsSL https://claude.ai/install.sh | bash -s -- 2.1.287
ENV PATH=/home/ubuntu/.local/bin:/home/ubuntu/go/bin:$PATH
# GOCACHE は tmpfs の /tmp へ（ルートディスクを圧迫しないため）。モジュールキャッシュは前取りして残す
ENV GOCACHE=/tmp/go-cache
RUN cd /tmp/modprefetch && go mod download all && go install golang.org/x/tools/gopls@latest && rm -rf /tmp/modprefetch
USER root
