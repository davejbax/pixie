FROM debian:bookworm-slim
RUN apt-get -y update && apt-get -y install build-essential autoconf automake xz-utils bison tar flex python3 gawk
WORKDIR /build
ARG GRUB_VERSION=2.12
ADD https://ftp.gnu.org/gnu/grub/grub-${GRUB_VERSION}.tar.xz grub.tar.xz
RUN xz --decompress --stdout grub.tar.xz | tar -xvf - --strip-components=1
ARG GRUB_TARGET=x86_64
RUN touch grub-core/extra_deps.lst && ./configure --with-platform=efi --target=$GRUB_TARGET && make 2>&1