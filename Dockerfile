FROM ubuntu:latest

RUN apt-get update && apt-get upgrade -y
RUN apt-get install curl git bzr -y

# install golang
RUN curl -s https://storage.googleapis.com/golang/go1.3.linux-amd64.tar.gz | tar -v -C /usr/local/ -xz

# path config
ENV PATH  $PATH:/usr/local/go/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin:/go/bin
ENV GOPATH  /go
ENV GOROOT  /usr/local/go

# ssh config
ADD ssh/known_hosts /root/.ssh/known_hosts
ADD ssh/id_rsa /root/.ssh/id_rsa
ADD ssh/id_rsa.pub /root/.ssh/id_rsa.pub
RUN chmod 700 /root/.ssh/id_rsa
RUN cp -R /root/.ssh /

RUN apt-get install fabric git -qqy

ADD . /go/src/github.com/ernado/cyhooks
RUN cd /go/src/github.com/ernado/cyhooks && go get .

ENTRYPOINT ["cyhooks"]
