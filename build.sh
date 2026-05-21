export PATH="/c/msys64/ucrt64/bin:$PATH"
export CGO_ENABLED=1
go build -o wetrace.exe .
