package cogs

import (
	"net/http"

	"github.com/getsops/sops/v3/decrypt"
)

func decryptFile(filePath string) ([]byte, error) {
	encData, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	format := FormatForPath(filePath)
	return decrypt.Data(encData, string(format))
}

func decryptHTTPFile(urlPath string, header http.Header, method, body string) ([]byte, error) {
	encData, err := getHTTPFile(urlPath, header, method, body)
	if err != nil {
		return nil, err
	}
	format := FormatForPath(urlPath)
	return decrypt.Data(encData, string(format))
}
