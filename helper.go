package dl

import (
	"log"
	"strings"

	"github.com/prajaraksh/sanitize"
)

var san = sanitize.New()

// extractName from URL
func extractName(URL string) string {
	// anything after last `/` is considered as name
	i := strings.LastIndexByte(URL, '/')
	if i == -1 {
		log.Println("Unable to extract name from URL", URL)
	}

	return san.Name(URL[i+1:])
}
