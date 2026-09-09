package wincore

import "testing"

func TestStoreSettings(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.GetSetting("deepseek.api_key"); got != "" {
		t.Fatalf("default setting = %q", got)
	}
	if err := store.SetSetting("deepseek.api_key", "test-key"); err != nil {
		t.Fatal(err)
	}
	if got := store.GetSetting("deepseek.api_key"); got != "test-key" {
		t.Fatalf("setting = %q", got)
	}
}
