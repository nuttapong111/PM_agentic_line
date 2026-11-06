package notion

import (
	"strings"
	"testing"
)

func TestHelpers(t *testing.T) {
	var types []string
	for _, b := range ToBlocks("# H\n- a\n[ ] todo\ntext") {
		types = append(types, b["type"].(string))
	}
	if got := strings.Join(types, ","); got != "heading_2,bulleted_list_item,to_do,paragraph" {
		t.Errorf("ToBlocks = %s", got)
	}
	if id := NormalizeID("https://www.notion.so/Retro-1a2b3c4d5e6f708192a3b4c5d6e7f809?pvs=4"); id != "1a2b3c4d5e6f708192a3b4c5d6e7f809" {
		t.Errorf("NormalizeID = %s", id)
	}
}
