package wincore

import "sync"

// SecretStore 是系统凭据存储:macOS 钥匙串、Windows 凭据管理器。
// Get 在没有该条目时返回 ok=false、err=nil。
type SecretStore interface {
	Get(name string) (value string, ok bool, err error)
	Set(name, value string) error
	Delete(name string) error
}

// secretSettings 是改存系统凭据、不再明文落 SQLite 的设置项。
var secretSettings = map[string]bool{"deepseek.api_key": true}

var (
	secretMu    sync.Mutex
	secretStore SecretStore = platformSecretStore()
)

// SetSecretStore 替换系统凭据存储;nil 表示退回明文设置库。macOS 桌面版用它
// 接入钥匙串(需要 cgo),Windows 默认即凭据管理器。
func SetSecretStore(s SecretStore) {
	secretMu.Lock()
	secretStore = s
	secretMu.Unlock()
}

func currentSecretStore() SecretStore {
	secretMu.Lock()
	defer secretMu.Unlock()
	return secretStore
}

// GetSetting 读取设置。敏感项优先读系统凭据;凭据里还没有、设置库里有旧的明文值时,
// 迁到系统凭据并清掉明文。系统凭据不可用时退回设置库,AI 功能不因此失效。
func (e *Engine) GetSetting(key string) string {
	store := currentSecretStore()
	if !secretSettings[key] || store == nil {
		return e.store.GetSetting(key)
	}
	value, ok, err := store.Get(key)
	if err == nil && ok {
		return value
	}
	legacy := e.store.GetSetting(key)
	if err == nil && legacy != "" && store.Set(key, legacy) == nil {
		_ = e.store.SetSetting(key, "")
	}
	return legacy
}

// SetSetting 保存设置。敏感项写入系统凭据(空值即删除),成功后清掉设置库里的明文;
// 系统凭据写入失败时返回错误,不悄悄退回明文存储。
func (e *Engine) SetSetting(key, value string) error {
	store := currentSecretStore()
	if !secretSettings[key] || store == nil {
		return e.store.SetSetting(key, value)
	}
	var err error
	if value == "" {
		err = store.Delete(key)
	} else {
		err = store.Set(key, value)
	}
	if err != nil {
		return err
	}
	return e.store.SetSetting(key, "")
}
