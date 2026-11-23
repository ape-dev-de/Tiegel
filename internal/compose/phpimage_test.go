package compose

import "testing"

func TestPHPImage(t *testing.T) {
	if !PHPImage("php:8.3-cli") || PHPImage("mariadb:11") || PHPImage("") {
		t.Fatal("PHPImage")
	}
}
