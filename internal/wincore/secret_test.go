package wincore

import (
	"errors"
	"testing"
)

type fakeSecrets struct {
	m    map[string]string
	fail bool
}

func (f *fakeSecrets) Get(name string) (string, bool, error) {
	if f.fail {
		return "", false, errors.New("keychain locked")
	}
	v, ok := f.m[name]
	return v, ok, nil
}
func (f *fakeSecrets) Set(name, value string) error {
	if f.fail {
		return errors.New("keychain locked")
	}
	f.m[name] = value
	return nil
}
func (f *fakeSecrets) Delete(name string) error { delete(f.m, name); return nil }

func withSecrets(t *testing.T, s SecretStore) {
	old := currentSecretStore()
	SetSecretStore(s)
	t.Cleanup(func() { SetSecretStore(old) })
}

func TestSecretSettingMigratesLegacyPlaintext(t *testing.T) {
	e, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	withSecrets(t, nil)
	if err := e.SetSetting("deepseek.api_key", "sk-old"); err != nil { // 旧版明文写入
		t.Fatal(err)
	}
	fake := &fakeSecrets{m: map[string]string{}}
	withSecrets(t, fake)
	if got := e.GetSetting("deepseek.api_key"); got != "sk-old" {
		t.Fatalf("get = %q", got)
	}
	if fake.m["deepseek.api_key"] != "sk-old" || e.store.GetSetting("deepseek.api_key") != "" {
		t.Fatalf("not migrated: keychain=%q sqlite=%q", fake.m["deepseek.api_key"], e.store.GetSetting("deepseek.api_key"))
	}
	if err := e.SetSetting("deepseek.api_key", "sk-new"); err != nil {
		t.Fatal(err)
	}
	if fake.m["deepseek.api_key"] != "sk-new" || e.store.GetSetting("deepseek.api_key") != "" || e.GetSetting("deepseek.api_key") != "sk-new" {
		t.Fatalf("set: keychain=%q sqlite=%q", fake.m["deepseek.api_key"], e.store.GetSetting("deepseek.api_key"))
	}
	if err := e.SetSetting("deepseek.api_key", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.m["deepseek.api_key"]; ok {
		t.Fatal("empty value should delete the credential")
	}
	// 非敏感项仍走设置库
	if err := e.SetSetting("deepseek.model", "m"); err != nil || e.store.GetSetting("deepseek.model") != "m" {
		t.Fatalf("plain setting: %v", err)
	}
}

func TestSecretSettingFallsBackWhenStoreUnavailable(t *testing.T) {
	e, err := New(t.TempDir(), nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	withSecrets(t, nil)
	_ = e.SetSetting("deepseek.api_key", "sk-old")
	fake := &fakeSecrets{m: map[string]string{}, fail: true}
	withSecrets(t, fake)
	if got := e.GetSetting("deepseek.api_key"); got != "sk-old" {
		t.Fatalf("fallback read = %q", got)
	}
	if e.store.GetSetting("deepseek.api_key") != "sk-old" {
		t.Fatal("plaintext must be kept when migration failed")
	}
	if err := e.SetSetting("deepseek.api_key", "sk-new"); err == nil {
		t.Fatal("write should fail loudly instead of silently storing plaintext")
	}
}
