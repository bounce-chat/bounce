all:
	cd protocol && \
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative,require_unimplemented_servers=false  \
		bounce.proto
	go build

install: all
	go install

deps:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.26
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.1

release:
	#GOOS=darwin GOARCH=amd64 go build -o bin/bounce-mac
	#GOOS=windows GOARCH=amd64 go build -o bin/bounce.exe
	GOOS=linux GOARCH=amd64 go build -o bin/bounce-linux
