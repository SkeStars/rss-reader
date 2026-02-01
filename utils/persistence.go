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
	// 后处理缓存文件
	PostProcessCacheFile = DataDir + "/postprocess_cache.json"
	// 条目缓存文件
	ItemsCacheFile = DataDir + "/items_cache.json"
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
	
	// PostProcessCache 后处理结果缓存
	PostProcessCache     map[string]models.PostProcessCacheEntry
	PostProcessCacheLock sync.RWMutex
	
	// 标记是否有未保存的更改
	dataChanged     bool
	dataChangedLock sync.Mutex
)

// InitPersistence 初始化持久化模块
func InitPersistence() {
	RankingTimestamps = make(map[string]string)
	PostProcessCache = make(map[string]models.PostProcessCacheEntry)
	
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
	// 加载后处理缓存
	loadPostProcessCache()
	// 加载条目缓存
	loadItemsCache()
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

	// 启动时延迟执行清理，防止离线期间配置变更导致的数据冗余
	// 只有在 DbMap 准备好后才执行，避免误删
	go func() {
		for i := 0; i < 12; i++ { // 最多尝试 1 分钟
			time.Sleep(5 * time.Second)
			if isDbMapReady() {
				CleanupRankingTimestampsOnConfigChange()
				CleanupPostProcessCacheOnConfigChange()
				CleanupReadStateOnConfigChange()
				CleanupItemsCacheOnConfigChange()
				return
			}
		}
	}()
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
	savePostProcessCache()
	saveItemsCache()
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

// loadPostProcessCache 加载后处理缓存
func loadPostProcessCache() {
	data, err := os.ReadFile(PostProcessCacheFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取后处理缓存文件失败: %v", err)
		}
		return
	}
	
	var cache map[string]models.PostProcessCacheEntry
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Printf("解析后处理缓存文件失败: %v", err)
		return
	}
	
	PostProcessCacheLock.Lock()
	PostProcessCache = cache
	PostProcessCacheLock.Unlock()
	
	log.Printf("已加载 %d 条后处理缓存", len(cache))
}

// savePostProcessCache 保存后处理缓存
func savePostProcessCache() {
	PostProcessCacheLock.RLock()
	data, err := json.MarshalIndent(PostProcessCache, "", "  ")
	PostProcessCacheLock.RUnlock()
	
	if err != nil {
		log.Printf("序列化后处理缓存失败: %v", err)
		return
	}
	
	if err := writeFileAtomic(PostProcessCacheFile, data); err != nil {
		log.Printf("保存后处理缓存失败: %v", err)
		return
	}
}

// loadItemsCache 加载条目缓存
func loadItemsCache() {
	data, err := os.ReadFile(ItemsCacheFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取条目缓存文件失败: %v", err)
		}
		return
	}
	
	var cache map[string][]models.Item
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Printf("解析条目缓存文件失败: %v", err)
		return
	}
	
	globals.ItemsCacheLock.Lock()
	globals.ItemsCache = cache
	globals.ItemsCacheLock.Unlock()
	
	log.Printf("已加载 %d 个源的条目缓存", len(cache))
}

// saveItemsCache 保存条目缓存
func saveItemsCache() {
	globals.ItemsCacheLock.RLock()
	data, err := json.MarshalIndent(globals.ItemsCache, "", "  ")
	globals.ItemsCacheLock.RUnlock()
	
	if err != nil {
		log.Printf("序列化条目缓存失败: %v", err)
		return
	}
	
	if err := writeFileAtomic(ItemsCacheFile, data); err != nil {
		log.Printf("保存条目缓存失败: %v", err)
		return
	}
}

// GetItemsCache 获取指定源的条目缓存
func GetItemsCache(rssURL string) ([]models.Item, bool) {
	globals.ItemsCacheLock.RLock()
	defer globals.ItemsCacheLock.RUnlock()
	items, ok := globals.ItemsCache[rssURL]
	return items, ok
}

// SetItemsCache 设置指定源的条目缓存
func SetItemsCache(rssURL string, items []models.Item) {
	globals.ItemsCacheLock.Lock()
	globals.ItemsCache[rssURL] = items
	globals.ItemsCacheLock.Unlock()
	MarkDataChanged()
}

// DeleteItemsCache 删除指定源的条目缓存
func DeleteItemsCache(rssURL string) {
	globals.ItemsCacheLock.Lock()
	delete(globals.ItemsCache, rssURL)
	globals.ItemsCacheLock.Unlock()
	MarkDataChanged()
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

// GetPostProcessCache 获取后处理缓存条目
func GetPostProcessCache(link string) (*models.PostProcessCacheEntry, bool) {
	PostProcessCacheLock.RLock()
	defer PostProcessCacheLock.RUnlock()
	entry, ok := PostProcessCache[link]
	if !ok {
		return nil, false
	}
	return &entry, true
}

// SetPostProcessCache 设置后处理缓存条目
func SetPostProcessCache(link string, entry models.PostProcessCacheEntry) {
	PostProcessCacheLock.Lock()
	PostProcessCache[link] = entry
	PostProcessCacheLock.Unlock()
	MarkDataChanged()
}

// DeletePostProcessCache 删除后处理缓存条目
func DeletePostProcessCache(link string) {
	PostProcessCacheLock.Lock()
	delete(PostProcessCache, link)
	PostProcessCacheLock.Unlock()
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
func CleanupRankingTimestamps(oldLinks []string, newLinks map[string]bool) {
	RankingTimestampsLock.Lock()
	defer RankingTimestampsLock.Unlock()
	
	changed := false
	for _, link := range oldLinks {
		if !newLinks[link] {
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
	
	for range ticker.C {
		// 每次清理前都检查 DbMap 是否有数据
		if isDbMapReady() {
			cleanupPersistentData()
		} else {
			log.Println("跳过定期清理：DbMap 为空，可能存在网络问题")
		}
	}
}

// isDbMapReady 检查 DbMap 是否已准备好（非空且有有效数据）
func isDbMapReady() bool {
	globals.Lock.RLock()
	defer globals.Lock.RUnlock()
	
	if len(globals.DbMap) == 0 {
		return false
	}
	
	// 检查是否至少有一个源有数据
	for _, feed := range globals.DbMap {
		if len(feed.Items) > 0 || len(feed.AllItemLinks) > 0 {
			return true
		}
	}
	return false
}

// cleanupPersistentData 清理持久化数据
func cleanupPersistentData() {
	log.Println("开始清理持久化数据...")
	
	// 收集所有当前有效的文章链接
	validLinks := collectValidArticleLinks()
	
	// 如果 validLinks 为空，说明 DbMap 可能还未初始化或网络有问题，跳过清理
	if len(validLinks) == 0 {
		log.Println("清理跳过：没有有效的文章链接（DbMap 可能为空）")
		return
	}
	
	// 清理过滤缓存
	cleanedFilterCache := cleanupFilterCache(validLinks)
	
	// 清理榜单时间戳
	cleanedRankingTimestamps := cleanupRankingTimestampsAll()
	
	// 清理已读状态
	cleanedReadState := cleanupReadState(validLinks)
	
	// 清理后处理缓存（只保留启用了后处理的源的缓存）
	validLinksWithPostProcess := collectValidLinksWithPostProcess()
	cleanedPostProcessCache := cleanupPostProcessCache(validLinksWithPostProcess)
	
	// 清理条目缓存（只保留启用了缓存的源）
	cleanedItemsCache := cleanupItemsCache()
	
	if cleanedFilterCache > 0 || cleanedRankingTimestamps > 0 || cleanedReadState > 0 || cleanedPostProcessCache > 0 || cleanedItemsCache > 0 {
		log.Printf("清理完成: 过滤缓存清理 %d 条，榜单时间戳清理 %d 条，已读状态清理 %d 条，后处理缓存清理 %d 条，条目缓存清理 %d 个源", 
			cleanedFilterCache, cleanedRankingTimestamps, cleanedReadState, cleanedPostProcessCache, cleanedItemsCache)
		MarkDataChanged()
		SaveAllData()
	} else {
		log.Println("清理完成: 无需清理的数据")
	}
}

// collectValidArticleLinks 收集所有当前有效的文章链接
// 包括所有RSS源返回的原始文章（过滤前），这样被过滤的文章缓存也会被保留
// 同时包含修改后的链接（后处理可能修改 Link）
func collectValidArticleLinks() map[string]bool {
	validLinks := make(map[string]bool)
	
	globals.Lock.RLock()
	for _, feed := range globals.DbMap {
		// 收集过滤前的所有文章（当前抓取周期）
		for _, link := range feed.AllItemLinks {
			validLinks[link] = true
		}
		// 收集当前展示的所有文章（可能包含缓存合并的旧文章）
		for _, item := range feed.Items {
			validLinks[item.Link] = true
			// 同时收集原始链接（后处理可能修改了 Link）
			if item.OriginalLink != "" {
				validLinks[item.OriginalLink] = true
			}
		}
	}
	globals.Lock.RUnlock()
	
	// 同时收集 ItemsCache 中的条目（这些是历史缓存条目，可能不在当前 DbMap 中）
	globals.ItemsCacheLock.RLock()
	for _, items := range globals.ItemsCache {
		for _, item := range items {
			validLinks[item.Link] = true
			if item.OriginalLink != "" {
				validLinks[item.OriginalLink] = true
			}
		}
	}
	globals.ItemsCacheLock.RUnlock()
	
	return validLinks
}

// collectRankingValidLinks 收集当前所有处于榜单模式源的有效文章链接
func collectRankingValidLinks() map[string]bool {
	validLinks := make(map[string]bool)
	
	// 收集所有处于榜单模式的源的URL
	rankingEnabledUrls := make(map[string]bool)
	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.RankingMode {
					rankingEnabledUrls[feedUrl.URL] = true
				}
			}
		} else if source.URL != "" && source.RankingMode {
			rankingEnabledUrls[source.URL] = true
		}
	}

	globals.Lock.RLock()
	for url, feed := range globals.DbMap {
		if rankingEnabledUrls[url] {
			for _, link := range feed.AllItemLinks {
				validLinks[link] = true
			}
			for _, item := range feed.Items {
				validLinks[item.Link] = true
				if item.OriginalLink != "" {
					validLinks[item.OriginalLink] = true
				}
			}
		}
	}
	globals.Lock.RUnlock()
	
	// 同时收集 ItemsCache 中榜单源的条目
	globals.ItemsCacheLock.RLock()
	for url, items := range globals.ItemsCache {
		if rankingEnabledUrls[url] {
			for _, item := range items {
				validLinks[item.Link] = true
				if item.OriginalLink != "" {
					validLinks[item.OriginalLink] = true
				}
			}
		}
	}
	globals.ItemsCacheLock.RUnlock()
	
	return validLinks
}

// collectValidLinksWithPostProcess 收集启用了后处理的RSS源的文章链接
func collectValidLinksWithPostProcess() map[string]bool {
	validLinks := make(map[string]bool)
	
	// 收集所有启用了后处理的源的URL
	postProcessEnabledUrls := make(map[string]bool)
	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.PostProcess != nil && feedUrl.PostProcess.Enabled {
					postProcessEnabledUrls[feedUrl.URL] = true
				} else if source.PostProcess != nil && source.PostProcess.Enabled && feedUrl.PostProcess == nil {
					postProcessEnabledUrls[feedUrl.URL] = true
				}
			}
		} else if source.URL != "" && source.PostProcess != nil && source.PostProcess.Enabled {
			postProcessEnabledUrls[source.URL] = true
		}
	}
	
	globals.Lock.RLock()
	for rssURL, feed := range globals.DbMap {
		// 检查该源是否启用了后处理
		if !postProcessEnabledUrls[rssURL] {
			continue
		}
		
		// 使用 AllItemLinks（过滤前的所有文章，原始链接）
		if len(feed.AllItemLinks) > 0 {
			for _, link := range feed.AllItemLinks {
				validLinks[link] = true
			}
		}
		// 同时收集 Items 中的链接（包括原始链接和修改后的链接）
		for _, item := range feed.Items {
			validLinks[item.Link] = true
			if item.OriginalLink != "" {
				validLinks[item.OriginalLink] = true
			}
		}
	}
	globals.Lock.RUnlock()
	
	// 同时收集 ItemsCache 中后处理源的条目
	globals.ItemsCacheLock.RLock()
	for url, items := range globals.ItemsCache {
		if postProcessEnabledUrls[url] {
			for _, item := range items {
				validLinks[item.Link] = true
				if item.OriginalLink != "" {
					validLinks[item.OriginalLink] = true
				}
			}
		}
	}
	globals.ItemsCacheLock.RUnlock()
	
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
func cleanupRankingTimestampsAll() int {
	validLinks := collectRankingValidLinks()
	
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

// cleanupPostProcessCache 清理后处理缓存中不再有效的条目
func cleanupPostProcessCache(validLinks map[string]bool) int {
	PostProcessCacheLock.Lock()
	defer PostProcessCacheLock.Unlock()
	
	cleaned := 0
	for link := range PostProcessCache {
		if !validLinks[link] {
			delete(PostProcessCache, link)
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

// CleanupPostProcessCacheOnConfigChange 配置变更时立即清理后处理缓存
func CleanupPostProcessCacheOnConfigChange() {	
	if !isDbMapReady() {
		return
	}
	// 收集启用了后处理的源的文章链接
	validLinksWithPostProcess := collectValidLinksWithPostProcess()
	
	// 清理后处理缓存
	cleaned := cleanupPostProcessCache(validLinksWithPostProcess)
	
	if cleaned > 0 {
		log.Printf("后处理缓存清理: 已清理 %d 条", cleaned)
		MarkDataChanged()
		savePostProcessCache()
	}
}

// cleanupItemsCache 清理条目缓存中不再启用缓存的源
func cleanupItemsCache() int {
	// 收集所有启用了缓存的源的URL
	validUrls := make(map[string]bool)
	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.CacheItems > 0 {
					validUrls[feedUrl.URL] = true
				}
			}
		} else if source.URL != "" && source.CacheItems > 0 {
			validUrls[source.URL] = true
		}
	}
	
	globals.ItemsCacheLock.Lock()
	defer globals.ItemsCacheLock.Unlock()
	
	cleaned := 0
	for url := range globals.ItemsCache {
		if !validUrls[url] {
			delete(globals.ItemsCache, url)
			cleaned++
		}
	}
	
	return cleaned
}

// GetCacheItems 获取指定URL的缓存条目数配置，返回0表示不缓存
func GetCacheItems(rssURL string) int {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.CacheItems
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == rssURL {
					return feedUrl.CacheItems
				}
			}
		}
	}
	return 0
}

// CleanupRankingTimestampsOnConfigChange 配置变更时立即清理不再处于榜单模式的源的时间戳
func CleanupRankingTimestampsOnConfigChange() {
	if !isDbMapReady() {
		return
	}
	
	cleaned := cleanupRankingTimestampsAll()

	if cleaned > 0 {
		log.Printf("榜单时间戳清理：配置变更导致 %d 条记录被清理", cleaned)
		dataChangedLock.Lock()
		dataChanged = true
		dataChangedLock.Unlock()
		saveRankingTimestamps()
	}
}

// CleanupItemsCacheOnConfigChange 配置变更时立即清理条目缓存
func CleanupItemsCacheOnConfigChange() {
	cleaned := cleanupItemsCache()
	
	if cleaned > 0 {
		log.Printf("条目缓存清理: 已清理 %d 个源", cleaned)
		MarkDataChanged()
		saveItemsCache()
	}
}

// CleanupReadStateOnConfigChange 配置变更时立即清理已读状态
func CleanupReadStateOnConfigChange() {
	if !isDbMapReady() {
		return
	}
	
	validLinks := collectValidArticleLinks()
	cleaned := cleanupReadState(validLinks)
	
	if cleaned > 0 {
		log.Printf("已读状态清理：配置变更导致 %d 条记录被清理", cleaned)
		MarkDataChanged()
		saveReadState()
	}
}
