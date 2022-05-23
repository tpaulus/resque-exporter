#!/bin/bash

PREFIX=$(pwd)/.build/
platforms=("386" "amd64" "arm64"  "mips" "mips64" "mips64le" "mipsle" "ppc64" "ppc64le" "s390x")
[ -d "${PREFIX}" ] && rm -rf ${PREFIX}
[ ! -d "${PREFIX}" ] && mkdir -p ${PREFIX}

for platform in ${platforms[*]}; do
  GOOS=linux GOARCH=${platform} PREFIX=${PREFIX} make build
  filename=resque-exporter-$(cat VERSION).linux-${platform}.tar.gz
  tar -cvzf ${PREFIX}${filename} -C ${PREFIX} resque-exporter
  # option --remove-files is not supported on macos, so we remove the file manually
  rm ${PREFIX}resque-exporter
  echo "`openssl sha256 ${PREFIX}${filename} | awk '{ print $2 }'` ${filename}" >> ${PREFIX}sha256sums.txt
done
