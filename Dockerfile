FROM quay.io/prometheus/golang-builder as builder

COPY . $GOPATH/src/github.com/jobscore/resque-exporter
WORKDIR $GOPATH/src/github.com/jobscore/resque-exporter
RUN go get github.com/prometheus/promu

RUN make PREFIX=/

FROM quay.io/prometheus/busybox

COPY --from=builder /resque-exporter /bin/resque-exporter

USER nobody:nogroup

EXPOSE 9447
ENTRYPOINT ["/bin/resque-exporter"]
