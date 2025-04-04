FROM opensuse/tumbleweed

RUN zypper in -y git go diffutils

RUN curl -L https://ziglang.org/download/0.14.0/zig-linux-x86_64-0.14.0.tar.xz > zig-linux-x86_64-0.14.0.tar.xz && \
    tar -xf zig-linux-x86_64-0.14.0.tar.xz && mkdir -p /opt && mkdir -p /mnt && \
    cp -r zig-linux-x86_64-0.14.0 /opt/

ENV PATH=$PATH:/opt/zig-linux-x86_64-0.14.0

COPY . /home/tux/fitbit-dump
RUN zypper addrepo --no-gpgcheck -f https://download.opensuse.org/repositories/home:nsekiguchi/openSUSE_Tumbleweed/home:nsekiguchi.repo && \
    zypper refresh && \
    zypper install -y arsh

# under Github Actions, regardless of WORKDIR setting, WORKDIR always indicates GITHUB_WORKSPACE (source code location)
# so, if create directory at WORKDIR, need root privilege
# (https://docs.github.com/en/actions/creating-actions/dockerfile-support-for-github-actions)
WORKDIR /home/tux/fitbit-dump/

CMD DIR="$(pwd)" && cd  /home/tux/fitbit-dump && git config --global --add safe.directory "${PWD}" && \
    arsh ./scripts/cross_compile.arsh && \
    cp fitbit-dump-* /mnt/ && (cp fitbit-dump-* "$DIR" || true)
