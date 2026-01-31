package utils

import (
	"encoding/json"
	"log"
	"os"
	"rss-reader/globals"
	"rss-reader/models"
	"sync"
	"time"
)

const (
	// 持久化数据目录
	DataDir = "/app/data"
	// 过滤缓存文件
	FilterCacheFile = DataDir + "/filter_cache.json"
	// 榜单模式时间戳文件
	RankingTimestampFile = DataDir + "/ranking_timestamps.json"
	// 已读状态文件（预留）
	ReadStateFile = DataDir + "/read_state.json"
	// 保存间隔（秒）
	SaveInterval = 60
	// 清理间隔（小时）
	CleanupInterval = 6
)

// PersistentData 持久化数据结构
type PersistentData struct {
	// FilterCache 过滤结果缓存
	FilterCache map[string]models.FilterCacheEntry `json:"filterCache"`
	// RankingTimestamps 榜单模式条目的更新时间戳 map[link]pubDate
	RankingTimestamps map[string]string `json:"rankingTimestamps"`
	// 保存时间
	SavedAt string `json:"savedAt"`
}

var (
	// RankingTimestamps 榜单模式条目的更新时间戳
	RankingTimestamps     map[string]string
	RankingTimestampsLock sync.RWMutex
	
	// 标记是否有未保存的更改
	dataChanged     bool
	dataChangedLock sync.Mutex
)

// InitPersistence 初始化持久化模块
func InitPersistence() {
	RankingTimestamps = make(map[string]string)
	
	// 确保数据目录存在
	ensureDataDir()
	
	// 加载已保存的数据
	loadPersistedData()
	
	// 启动定期保存任务
	go autoSaveLoop()
	
	// 启动定期清理任务
	go autoCleanupLoop()
}

// ensureDataDir 确保数据目录存在
func ensureDataDir() {
	if _, err := os.Stat(DataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(DataDir, 0755); err != nil {
			log.Printf("创建数据目录失败: %v", err)
		}
	}
}

// loadPersistedData 加载持久化的数据
func loadPersistedData() {
	// 加载过滤缓存
	loadFilterCache()
	// 加载榜单时间戳
	loadRankingTimestamps()
	// 加载已读状态
	loadReadState()
}

// loadFilterCache 加载过滤缓存
func loadFilterCache() {
	data, err := os.ReadFile(FilterCacheFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取过滤缓存文件失败: %v", err)
		}
		return
	}
	
	var cache map[string]models.FilterCacheEntry
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Printf("解析过滤缓存文件失败: %v", err)
		return
	}
	
	globals.FilterCacheLock.Lock()
	globals.FilterCache = cache
	globals.FilterCacheLock.Unlock()
	
	log.Printf("已加载 %d 条过滤缓存", len(cache))
}

// loadRankingTimestamps 加载榜单时间戳
func loadRankingTimestamps() {
	data, err := os.ReadFile(RankingTimestampFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取榜单时间戳文件失败: %v", err)
		}
		return
	}
	
	var timestamps map[string]string
	if err := json.Unmarshal(data, &timestamps); err != nil {
		log.Printf("解析榜单时间戳文件失败: %v", err)
		return
	}
	
	RankingTimestampsLock.Lock()
	RankingTimestamps = timestamps
	RankingTimestampsLock.Unlock()
	
	log.Printf("已加载 %d 条榜单时间戳", len(timestamps))
}

// loadReadState 加载已读状态
func loadReadState() {
	data, err := os.ReadFile(ReadStateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取已读状态文件失败: %v", err)
		}
		return
	}
	
	var readState map[string]int64
	if err := json.Unmarshal(data, &readState); err != nil {
		log.Printf("解析已读状态文件失败: %v", err)
		return
	}
	
	globals.ReadStateLock.Lock()
	globals.ReadState = readState
	globals.ReadStateLock.Unlock()
	
	log.Printf("已加载 %d 条已读状态", len(readState))
}

// MarkDataChanged 标记数据已更改
func MarkDataChanged() {
	dataChangedLock.Lock()
	dataChanged = true
	dataChangedLock.Unlock()
}

// autoSaveLoop 自动保存循环
func autoSaveLoop() {
	ticker := time.NewTicker(time.Duration(SaveInterval) * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		dataChangedLock.Lock()
		needSave := dataChanged
		dataChanged = false
		dataChangedLock.Unlock()
		
		if needSave {
			SaveAllData()
		}
	}
}

// SaveAllData 保存所有数据
func SaveAllData() {
	saveFilterCache()
	saveRankingTimestamps()
	saveReadState()
}

// saveFilterCache 保存过滤缓存
func saveFilterCache() {
	globals.FilterCacheLock.RLock()
	data, err := json.MarshalIndent(globals.FilterCache, "", "  ")
	globals.FilterCacheLock.RUnlock()
	
	if err != nil {
		log.Printf("序列化过滤缓存失败: %v", err)
		return
	}
	
	if err := writeFileAtomic(FilterCacheFile, data); err != nil {
		log.Printf("保存过滤缓存失败: %v", err)
		return
	}
}

// saveRankingTimestamps 保存榜单时间戳
func saveRankingTimestamps() {
	RankingTimestampsLock.RLock()
	data, err := json.MarshalIndent(RankingTimestamps, "", "  ")
	RankingTimestampsLock.RUnlock()
	
	if err != nil {
		log.Printf("序列化榜单时间戳失败: %v", err)
		return
	}
	
	if err := writeFileAtomic(RankingTimestampFile, data); err != nil {
		log.Printf("保存榜单时间戳失败: %v", err)
		return
	}
}

// saveReadState 保存已读状态
func saveReadState() {
	globals.ReadStateLock.RLock()
	data, err := json.MarshalIndent(globals.ReadState, "", "  ")
	globals.ReadStateLock.RUnlock()
	
	if err != nil {
		log.Printf("序列化已读状态失败: %v", err)
		return
	}
	
	if err := writeFileAtomic(ReadStateFile, data); err != nil {
		log.Printf("保存已读状态失败: %v", err)
		return
	}
}

// writeFileAtomic 原子写入文件（先写临时文件再重命名）
func writeFileAtomic(filePath string, data []byte) error {
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, filePath)
}

// GetRankingTimestamp 获取榜单条目的时间戳
func GetRankingTimestamp(link string) (string, bool) {
	RankingTimestampsLock.RLock()
	defer RankingTimestampsLock.RUnlock()
	ts, ok := RankingTimestamps[link]
	return ts, ok
}

// SetRankingTimestamp 设置榜单条目的时间戳
func SetRankingTimestamp(link, timestamp string) {
	RankingTimestampsLock.Lock()
	RankingTimestamps[link] = timestamp
	RankingTimestampsLock.Unlock()
	MarkDataChanged()
}

// DeleteRankingTimestamp 删除榜单条目的时间戳
func DeleteRankingTimestamp(link string) {
	RankingTimestampsLock.Lock()
	delete(RankingTimestamps, link)
	RankingTimestampsLock.Unlock()
	MarkDataChanged()
}

// GetReadState 获取所有已读状态
func GetReadState() map[string]int64 {
	globals.ReadStateLock.RLock()
	defer globals.ReadStateLock.RUnlock()
	
	// 返回副本避免并发问题
	result := make(map[string]int64, len(globals.ReadState))
	for k, v := range globals.ReadState {
		result[k] = v
	}
	return result
}

// IsRead 检查文章是否已读
func IsRead(link string) bool {
	globals.ReadStateLock.RLock()
	defer globals.ReadStateLock.RUnlock()
	_, ok := globals.ReadState[link]
	return ok
}

// MarkRead 标记文章为已读
func MarkRead(link string) {
	globals.ReadStateLock.Lock()
	globals.ReadState[link] = time.Now().Unix()
	globals.ReadStateLock.Unlock()
	MarkDataChanged()
}

// MarkReadBatch 批量标记文章为已读
func MarkReadBatch(links []string) {
	globals.ReadStateLock.Lock()
	now := time.Now().Unix()
	for _, link := range links {
		globals.ReadState[link] = now
	}
	globals.ReadStateLock.Unlock()
	MarkDataChanged()
}

// MarkUnread 标记文章为未读
func MarkUnread(link string) {
	globals.ReadStateLock.Lock()
	delete(globals.ReadState, link)
	globals.ReadStateLock.Unlock()
	MarkDataChanged()
}

// ClearAllReadState 清除所有已读状态
func ClearAllReadState() {
	globals.ReadStateLock.Lock()
	globals.ReadState = make(map[string]int64)
	globals.ReadStateLock.Unlock()
	MarkDataChanged()
}

// CleanupRankingTimestamps 清理不再存在的榜单条目时间戳
func CleanupRankingTimestamps(validLinks map[string]bool) {
	RankingTimestampsLock.Lock()
	defer RankingTimestampsLock.Unlock()
	
	changed := false
	for link := range RankingTimestamps {
		if !validLinks[link] {
			delete(RankingTimestamps, link)
			changed = true
		}
	}
	
	if changed {
		dataChangedLock.Lock()
		dataChanged = true
		dataChangedLock.Unlock()
	}
}

// Shutdown 关闭时保存数据
func Shutdown() {
	log.Println("正在保存持久化数据...")
	SaveAllData()
	log.Println("持久化数据保存完成")
}

// autoCleanupLoop 自动清理循环
func autoCleanupLoop() {
	ticker := time.NewTicker(time.Duration(CleanupInterval) * time.Hour)
	defer ticker.Stop()
	
	// 启动后延迟一段时间再执行第一次清理，避免启动时负载过高
	time.Sleep(5 * time.Minute)
	cleanupPersistentData()
	
	for range ticker.C {
		cleanupPersistentData()
	}
}

// cleanupPersistentData 清理持久化数据
func cleanupPersistentData() {
	log.Println("开始清理持久化数据...")
	
	// 收集所有当前有效的文章链接
	validLinks := collectValidArticleLinks()
	
	// 清理过滤缓存
	cleanedFilterCache := cleanupFilterCache(validLinks)
	
	// 清理榜单时间戳
	cleanedRankingTimestamps := cleanupRankingTimestampsAll(validLinks)
	
	// 清理已读状态
	cleanedReadState := cleanupReadState(validLinks)
	
	if cleanedFilterCache > 0 || cleanedRankingTimestamps > 0 || cleanedReadState > 0 {
		log.Printf("清理完成: 过滤缓存清理 %d 条，榜单时间戳清理 %d 条，已读状态清理 %d 条", cleanedFilterCache, cleanedRankingTimestamps, cleanedReadState)
		MarkDataChanged()
		SaveAllData()
	} else {
		log.Println("清理完成: 无需清理的数据")
	}
}

// collectValidArticleLinks 收集所有当前有效的文章链接
// 包括所有RSS源返回的原始文章（过滤前），这样被过滤的文章缓存也会被保留
func collectValidArticleLinks() map[string]bool {
	validLinks := make(map[string]bool)
	
	globals.Lock.RLock()
	defer globals.Lock.RUnlock()
	
	for _, feed := range globals.DbMap {
		// 使用 AllItemLinks（过滤前的所有文章）而不是 Items（过滤后的）
		// 这样可以保留被过滤文章的缓存，避免下次更新时重复AI判断
		if len(feed.AllItemLinks) > 0 {
			for _, link := range feed.AllItemLinks {
				validLinks[link] = true
			}
		} else {
			// 兼容旧数据：如果没有 AllItemLinks，使用 Items
			for _, item := range feed.Items {
				validLinks[item.Link] = true
			}
		}
	}
	
	return validLinks
}

// cleanupFilterCache 清理过滤缓存中不再有效的条目
// 注意：由于被过滤的文章也应该保留缓存（避免重复AI判断），
// 这里的 validLinks 已经包含了 FilterCache 中的所有链接
func cleanupFilterCache(validLinks map[string]bool) int {
	globals.FilterCacheLock.Lock()
	defer globals.FilterCacheLock.Unlock()
	
	cleaned := 0
	for link := range globals.FilterCache {
		if !validLinks[link] {
			delete(globals.FilterCache, link)
			cleaned++
		}
	}
	
	return cleaned
}

// cleanupRankingTimestampsAll 清理榜单时间戳中不再有效的条目
func cleanupRankingTimestampsAll(validLinks map[string]bool) int {
	RankingTimestampsLock.Lock()
	defer RankingTimestampsLock.Unlock()
	
	cleaned := 0
	for link := range RankingTimestamps {
		if !validLinks[link] {
			delete(RankingTimestamps, link)
			cleaned++
		}
	}
	
	return cleaned
}

// cleanupReadState 清理已读状态中不再有效的条目
func cleanupReadState(validLinks map[string]bool) int {
	globals.ReadStateLock.Lock()
	defer globals.ReadStateLock.Unlock()
	
	cleaned := 0
	for link := range globals.ReadState {
		if !validLinks[link] {
			delete(globals.ReadState, link)
			cleaned++
		}
	}
	
	return cleaned
}

// SaveConfig 保存配置到 config.json
func SaveConfig(config models.Config) error {
	data, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return err
	}
	
	// Docker Bind Mount 不支持 atomic rename 覆盖文件，必须直接写入
	// 使用 O_TRUNC 清空原有内容
	f, err := os.OpenFile("config.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	
	_, err = f.Write(data)
	return err
}
