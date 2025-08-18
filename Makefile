#phony targets
.PHONY: build clean

#build the binary for windows
build-windows:
	GOOS=windows GOARCH=amd64 go build -o image-convert.exe main.go

#build the binary for linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -o image-convert main.go

#build the binary for mac
build-mac:
	GOOS=darwin GOARCH=amd64 go build -o image-convert main.go

#clean the binary
clean:
	rm -f image-convert

#copy binary to the bin directory
copy:
	cp ./image-convert /usr/local/bin/