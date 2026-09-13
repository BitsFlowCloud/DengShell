# The base digest and Go archive are pinned. Reuse/export this builder image to
# reproduce its full dependency set; package versions are recorded in build-info.
FROM ubuntu:22.04@sha256:829f6df217bcbae2b371026e81711d1a787c61b2967ad09d015063663ebafbf7
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev ca-certificates curl python3 rpm dpkg-dev xz-utils xvfb xauth dbus-x11 iputils-ping && rm -rf /var/lib/apt/lists/*
RUN curl --fail --location --retry 3 https://go.dev/dl/go1.27.1.linux-amd64.tar.gz -o /var/tmp/go.tar.gz \
 && echo '63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445  /var/tmp/go.tar.gz' | sha256sum -c - \
 && tar -C /usr/local -xzf /var/tmp/go.tar.gz && rm /var/tmp/go.tar.gz
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local
