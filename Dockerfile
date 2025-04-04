FROM opensuse/tumbleweed

RUN zypper in -y git go1.22 diffutils

RUN curl -L https://ziglang.org/download/0.14.0/zig-linux-x86_64-0.14.0.tar.xz > zig-linux-x86_64-0.14.0.tar.xz && \
    tar -xf zig-linux-x86_64-0.14.0.tar.xz && mkdir -p /opt && cp -r zig-linux-x86_64-0.14.0 /opt/

ENV PATH=$PATH:/opt/zig-linux-x86_64-0.14.0

COPY . /home/tux/fitbit-dump
RUN zypper addrepo --no-gpgcheck -f https://download.opensuse.org/repositories/home:nsekiguchi/openSUSE_Tumbleweed/home:nsekiguchi.repo && \
    zypper refresh && \
    zypper install -y arsh

WORKDIR /home/tux/fitbit-dump/

CMD ls ./ && arsh ./scripts/cross_compile.arsh && cp fitbit-dump-* /mnt/
