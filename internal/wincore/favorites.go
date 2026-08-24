package wincore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxSendHistory = 20

// favoritesFile 返回收藏报文的持久化路径(data 目录下的 favorites.json)。
func (e *Engine) favoritesFile() string {
	return filepath.Join(e.store.Dir(), "favorites.json")
}

// LoadFavorites 从磁盘加载收藏报文。
func (e *Engine) LoadFavorites() {
	data, err := os.ReadFile(e.favoritesFile())
	if err != nil {
		return
	}
	e.histMu.Lock()
	_ = json.Unmarshal(data, &e.favorites)
	if e.favorites == nil {
		e.favorites = map[string]string{}
	}
	e.histMu.Unlock()
}

// FavoriteNames 返回收藏报文名(升序)。
func (e *Engine) FavoriteNames() []string {
	e.histMu.Lock()
	defer e.histMu.Unlock()
	names := make([]string, 0, len(e.favorites))
	for n := range e.favorites {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Favorite 返回指定收藏报文内容。
func (e *Engine) Favorite(name string) string {
	e.histMu.Lock()
	defer e.histMu.Unlock()
	return e.favorites[name]
}

// SaveFavorite 新增或更新收藏报文并落盘。
func (e *Engine) SaveFavorite(name, value string) error {
	e.histMu.Lock()
	if e.favorites == nil {
		e.favorites = map[string]string{}
	}
	e.favorites[name] = value
	data, err := json.Marshal(e.favorites)
	e.histMu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(e.favoritesFile(), data, 0o600)
}

// DeleteFavorite 删除收藏报文并落盘。
func (e *Engine) DeleteFavorite(name string) error {
	e.histMu.Lock()
	delete(e.favorites, name)
	data, err := json.Marshal(e.favorites)
	e.histMu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(e.favoritesFile(), data, 0o600)
}

// rememberSend 记录发送历史(去重,保留最近 maxSendHistory 条,新→旧)。
func (e *Engine) rememberSend(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	e.histMu.Lock()
	defer e.histMu.Unlock()
	for i, s := range e.sendHistory {
		if s == input {
			e.sendHistory = append(e.sendHistory[:i], e.sendHistory[i+1:]...)
			break
		}
	}
	e.sendHistory = append([]string{input}, e.sendHistory...)
	if len(e.sendHistory) > maxSendHistory {
		e.sendHistory = e.sendHistory[:maxSendHistory]
	}
}

// RecentSends 返回最近的发送历史(新→旧)。
func (e *Engine) RecentSends() []string {
	e.histMu.Lock()
	defer e.histMu.Unlock()
	return append([]string(nil), e.sendHistory...)
}
