package chat

import "testing"

func TestCleanGeneratedTitle(t *testing.T) {
	if got := cleanGeneratedTitle("Title: Quiet Systems That Scale\n"); got != "Quiet Systems That Scale" {
		t.Fatalf("title = %q", got)
	}
	if cleanGeneratedTitle("one") != "" || cleanGeneratedTitle("folder/name") != "" {
		t.Fatal("invalid generated title was accepted")
	}
}

func TestExplicitTitlePreventsCreativeRewrite(t *testing.T) {
	if !explicitTitle(`create a note titled "Rumera launch"`, "Rumera launch") {
		t.Fatal("explicit title was not detected")
	}
	if explicitTitle("create a note about launch risks", "Launch Risk Compass") {
		t.Fatal("implicit title was treated as explicit")
	}
}
