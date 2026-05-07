package pricing

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

//go:embed prices.json
var embeddedPrices embed.FS

// CCXPricesFileEnv 文件 override 环境变量。
//
// 设置该变量为绝对路径或工作目录相对路径时，loader 启动会优先用该文件覆盖
// embed.FS 内置价格表，并通过 fsnotify 监听文件变化实现热重载。
const CCXPricesFileEnv = "CCX_PRICES_FILE"

// pricesFile 是 prices.json 的反序列化容器。
//
// 顶层除 ModelPrice 切片外还包含 _lastVerified / _source 元信息（implement 阶段
// 二次校验时供 ops 排查），decimal 序列化保持字符串。
type pricesFile struct {
	LastVerified string       `json:"_lastVerified,omitempty"`
	Source       string       `json:"_source,omitempty"`
	Models       []ModelPrice `json:"models"`
}

// Loader 价格表加载器，提供查询、热重载与版本指纹能力。
//
// 并发安全：内部读写锁保护价格映射；调用方可在请求路径无锁调用 GetPrice。
type Loader struct {
	mu       sync.RWMutex
	prices   map[string]*ModelPrice
	version  string // 当前价格表 sha256 前 12 位
	source   string // "embed" 或文件绝对路径
	filePath string // 文件 override 路径（空表示仅 embed）
	watcher  *fsnotify.Watcher
	closeCh  chan struct{}
}

// NewLoader 构建 Loader。
//
// 行为：
//  1. 总是先加载内置 embed.FS 中的 prices.json 作为兜底；
//  2. 读取环境变量 CCX_PRICES_FILE，存在且文件可读时用文件覆盖 embed；
//  3. 文件 override 模式下启动 fsnotify 监听，文件变更时调用 reload；
//  4. 文件 override 模式下若启动时文件解析失败，返回错误（不静默回退）；
//     运行期 reload 失败则保留旧表，仅打日志告警。
//
// 不需要文件 override 时可传 filePath = ""，此时仅用 embed。
func NewLoader(filePath string) (*Loader, error) {
	l := &Loader{
		filePath: filePath,
		closeCh:  make(chan struct{}),
	}

	// 1) 加载 embed 兜底
	embedBytes, err := embeddedPrices.ReadFile("prices.json")
	if err != nil {
		return nil, fmt.Errorf("pricing.NewLoader: 读取内置 prices.json 失败: %w", err)
	}
	embedPrices, embedVer, err := parsePrices(embedBytes)
	if err != nil {
		return nil, fmt.Errorf("pricing.NewLoader: 解析内置 prices.json 失败: %w", err)
	}
	l.prices = embedPrices
	l.version = embedVer
	l.source = "embed"

	// 2) 文件 override
	if filePath != "" {
		abs, absErr := filepath.Abs(filePath)
		if absErr == nil {
			filePath = abs
			l.filePath = abs
		}
		if err := l.loadFromFile(filePath); err != nil {
			return nil, fmt.Errorf("pricing.NewLoader: 加载 override 文件 %s 失败: %w", filePath, err)
		}
		// 3) 启动热重载
		if err := l.startWatcher(); err != nil {
			log.Printf("[Pricing-Reload] 启动 fsnotify 失败 (将仅使用启动时价格): %v", err)
		}
	}
	return l, nil
}

// NewLoaderFromEnv 从环境变量 CCX_PRICES_FILE 读取 override 路径并构造 Loader。
//
// 该函数是 NewLoader 的便捷封装，main 包应优先调用本函数。
func NewLoaderFromEnv() (*Loader, error) {
	return NewLoader(os.Getenv(CCXPricesFileEnv))
}

// GetPrice 查询模型价格表；不存在则返回 (nil, false)。
func (l *Loader) GetPrice(modelID string) (*ModelPrice, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p, ok := l.prices[modelID]
	return p, ok
}

// Version 当前价格表的版本指纹（sha256 前 12 位），用于 UsageRecord.PriceVersion。
func (l *Loader) Version() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.version
}

// Source 当前价格表来源："embed" 或文件绝对路径。仅供调试/管理 API 使用。
func (l *Loader) Source() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.source
}

// Close 停止 fsnotify 监听并释放资源。
func (l *Loader) Close() error {
	if l.watcher == nil {
		return nil
	}
	close(l.closeCh)
	return l.watcher.Close()
}

// loadFromFile 从文件加载并替换内存价格表（线程安全）。
func (l *Loader) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	prices, ver, err := parsePrices(data)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.prices = prices
	l.version = ver
	l.source = path
	l.mu.Unlock()
	return nil
}

// startWatcher 启动 fsnotify 监听文件目录变化（监听目录而非文件以兼容 atomic write）。
func (l *Loader) startWatcher() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.filePath)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return err
	}
	l.watcher = w
	go l.watchLoop()
	return nil
}

// watchLoop 接收 fsnotify 事件并触发 reload；reload 失败时保留旧表 + 日志告警。
func (l *Loader) watchLoop() {
	target := l.filePath
	for {
		select {
		case <-l.closeCh:
			return
		case ev, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if ev.Name != target {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if err := l.loadFromFile(target); err != nil {
				log.Printf("[Pricing-Reload] 重新加载失败，保留旧价格表: %v", err)
				continue
			}
			log.Printf("[Pricing-Reload] 价格表已更新: version=%s source=%s", l.Version(), l.Source())
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[Pricing-Reload] watcher 错误: %v", err)
		}
	}
}

// parsePrices 解析 prices.json 字节流，并返回 modelID 索引 map + 版本指纹。
func parsePrices(data []byte) (map[string]*ModelPrice, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("prices.json 为空")
	}
	var f pricesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", fmt.Errorf("解析 prices.json 失败: %w", err)
	}
	if len(f.Models) == 0 {
		return nil, "", errors.New("prices.json 不含任何模型")
	}
	out := make(map[string]*ModelPrice, len(f.Models))
	for i := range f.Models {
		mp := f.Models[i]
		if err := mp.Validate(); err != nil {
			return nil, "", fmt.Errorf("校验模型 %s 失败: %w", mp.ModelID, err)
		}
		// 复制到堆上以便 GetPrice 返回稳定指针。
		cp := mp
		out[mp.ModelID] = &cp
	}
	sum := sha256.Sum256(data)
	ver := hex.EncodeToString(sum[:])[:12]
	return out, ver, nil
}
