FROM debian:bookworm-slim
RUN apt-get install build-essential autoconf automake xz-utils bison tar flex python3
WORKDIR /build
ARG GRUB_VERSION=2.12
ADD https://ftp.gnu.org/gnu/grub/grub-${GRUB_VERSION}.tar.xz grub.tar.xz
RUN xz --decompress --stdout grub.tar.xz | tar -xzvf - --strip-components=1