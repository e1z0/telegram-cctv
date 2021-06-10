# This how we want to name the binary output
BINARY=client

VERSION=`cat VERSION`
BUILD=`date "+%F-%T"`
COMMIT=`git rev-parse HEAD`

# Setup the -ldflags option for go build here, interpolate the variable values
LDFLAGS_f1=-ldflags "-w -s -X main.Version=${VERSION} -X main.Build=${BUILD} -X main.Commit=${COMMIT}"

all: build

run:
	go build ${LDFLAGS_f1} -o $(BINARY) -v ./...
	./${BINARY}
deps:
	go get github.com/eclipse/paho.mqtt.golang
	go get github.com/go-telegram-bot-api/telegram-bot-api
	go get github.com/nsf/termbox-go
	go get github.com/go-sql-driver/mysql
	go get github.com/jmoiron/sqlx
build:
	go build ${LDFLAGS_f1} -o ${BINARY}

# Installs our project: copies binaries
install:
	go install ${LDFLAGS_f1}

# Cleans our project: deletes binaries
clean:
	if [ -f ${BINARY} ] ; then rm ${BINARY} ; fi

.PHONY: clean install

