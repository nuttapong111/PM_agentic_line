package crypto

import "testing"

func TestRoundTrip(t *testing.T) {
	c := New("k3y")
	if got, ok := c.Decrypt(c.Encrypt("secret-ä-ข")); !ok || got != "secret-ä-ข" {
		t.Fatalf("round trip: %q %v", got, ok)
	}
	if _, ok := c.Decrypt("v1.aaa.bbb.ccc"); ok {
		t.Fatal("garbage decrypted")
	}
	if _, ok := New("other").Decrypt(c.Encrypt("x")); ok {
		t.Fatal("wrong key decrypted")
	}
}

// A blob written by the old TypeScript version (same key derivation and format) still opens.
func TestReadsBlobFromTypeScriptVersion(t *testing.T) {
	got, ok := New("k3y").Decrypt("v1.U2C002pn0pixDm_6.QMc6KjBNXZkwgLYau7Xsew.NNjybLy0G-gr7bNlrg")
	if !ok || got != "secret-ä-ข" {
		t.Fatalf("got %q %v", got, ok)
	}
}
