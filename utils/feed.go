package utils

import (
	"log"
	"net/url"
	"rss-reader/globals"
	"rss-reader/models"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mmcdole/gofeed"
	"sync"
	"fmt"
)

func UpdateFeeds() {
	var (
		tick = time.Tick(time.Duration(globals.RssUrls.ReFresh) * time.Minute)
	)
	for {
		formattedTime := time.Now().Format("2006-01-02 15:04:05")
		for _, source := range globals.RssUrls.Sources {
			if source.IsFolder() {
				for _, feedUrl := range source.Urls {
					go UpdateFeed(feedUrl.URL, formattedTime)
				}
			} else if source.URL != "" {
				go UpdateFeed(source.URL, formattedTime)
			}
		}
		<-tick
	}
}

// GetFaviconURL 根据 RSS URL 获取对应的 favicon URL
func GetFaviconURL(rssURL string) string {
	parsedURL, err := url.Parse(rssURL)
	if err != nil {
		return ""
	}
	// 使用 Google 的 favicon 服务
	if parsedURL.Host != "" {
		return "https://www.google.com/s2/favicons?domain=" + parsedURL.Host + "&sz=64"
	}
	return ""
}

// IsRankingMode 检查指定URL是否启用了榜单模式
func IsRankingMode(rssURL string) bool {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.RankingMode
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == rssURL {
					return feedUrl.RankingMode
				}
			}
		}
	}
	return false
}

// GetMaxItems 获取指定URL的最大读取条目数限制，返回0表示不限制
func GetMaxItems(rssURL string) int {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.MaxItems
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == rssURL {
					return feedUrl.MaxItems
				}
			}
		}
	}
	return 0
}

// GetCustomIconURL 从配置中获取自定义图标，如果没有则自动获取 favicon
func GetCustomIconURL(rssURL string, customIcon string) string {
	if customIcon != "" {
		return customIcon
	}
	return GetFaviconURL(rssURL)
}

func UpdateFeed(url, formattedTime string) error {
	result, err := globals.Fp.ParseURL(url)
	if err != nil {
		log.Printf("Error fetching feed: %v | %v", url, err)
		return err
	}
	
	// 检查是否为榜单模式
	isRanking := IsRankingMode(url)
	
	//feed内容无更新时无需更新缓存（非榜单模式下的快速判断）
	if !isRanking {
		globals.Lock.RLock()
		cache, ok := globals.DbMap[url]
		globals.Lock.RUnlock()
		
		if ok &&
			len(result.Items) > 0 &&
			len(cache.Items) > 0 &&
			result.Items[0].Link == cache.Items[0].Link {
			return nil
		}
	}
	
	// 获取图标：优先级 1.配置的自定义图标 2.RSS feed的image 3.自动生成favicon
	icon := GetIconForFeed(url, result)
	
	// 先构建所有Items
	allItems := make([]models.Item, 0, len(result.Items))
	for _, v := range result.Items {
		pubDate := ""
		if v.PublishedParsed != nil {
			pubDate = v.PublishedParsed.Format("2006-01-02 15:04:05")
		} else if v.UpdatedParsed != nil {
			pubDate = v.UpdatedParsed.Format("2006-01-02 15:04:05")
		}
		allItems = append(allItems, models.Item{
			Link:        v.Link,
			Title:       v.Title,
			Description: v.Description,
			Source:      result.Title,
			PubDate:     pubDate,
		})
	}

	// 应用最大条目数限制
	maxItems := GetMaxItems(url)
	if maxItems > 0 && len(allItems) > maxItems {
		allItems = allItems[:maxItems]
	}

	// 榜单模式下的处理：将新增条目放在前面
	if isRanking {
		allItems = processRankingItems(url, allItems)
	}

	// 应用AI过滤
	originalCount := len(allItems)
	filteredItems := allItems
	if ShouldFilter(url) {
		filteredItems = FilterItems(allItems, url)
	}

	// 记录过滤前的所有文章链接，用于清理时判断
	allItemLinks := make([]string, 0, len(allItems))
	for _, item := range allItems {
		allItemLinks = append(allItemLinks, item.Link)
	}

	customFeed := models.Feed{
		Title:         result.Title,
		Link:          url,
		Icon:          icon,
		Custom:        map[string]string{"lastupdate": formattedTime},
		Items:         filteredItems,
		FilteredCount: originalCount - len(filteredItems),
		AllItemLinks:  allItemLinks,
	}

	globals.Lock.Lock()
	defer globals.Lock.Unlock()
	globals.DbMap[url] = customFeed
	return nil
}

// processRankingItems 处理榜单模式的条目：将新增条目放在前面，并清理过时的缓存
func processRankingItems(url string, newItems []models.Item) []models.Item {
	globals.Lock.RLock()
	cache, hasCached := globals.DbMap[url]
	globals.Lock.RUnlock()
	
	// 构建新条目的链接集合，用于清理过期缓存
	newLinks := make(map[string]bool)
	for _, item := range newItems {
		newLinks[item.Link] = true
	}
	
	// 如果没有内存缓存，尝试从持久化存储恢复时间戳
	if !hasCached || len(cache.Items) == 0 {
		// 从持久化存储恢复时间戳
		for i, item := range newItems {
			if ts, ok := GetRankingTimestamp(item.Link); ok {
				newItems[i].PubDate = ts
			} else {
				// 新条目，设置当前时间并持久化
				nowTs := time.Now().Format("2006-01-02 15:04:05")
				newItems[i].PubDate = nowTs
				SetRankingTimestamp(item.Link, nowTs)
			}
		}
		return newItems
	}
	
	// 构建旧条目的链接集合，用于快速查找
	oldLinks := make(map[string]bool)
	for _, item := range cache.Items {
		oldLinks[item.Link] = true
	}
	
	// 分离新增条目和已存在条目
	addedItems := make([]models.Item, 0)
	existingItems := make([]models.Item, 0)
	
	// 为新增条目设置更新时间作为时间戳
	rankingNewTimestamp := time.Now().Format("2006-01-02 15:04:05")
	
	for i, item := range newItems {
		if !oldLinks[item.Link] {
			// 这是新增的条目
			// 检查持久化存储中是否有时间戳（服务重启后恢复）
			if ts, ok := GetRankingTimestamp(item.Link); ok {
				item.PubDate = ts
			} else {
				// 真正的新条目，设置当前时间并持久化
				item.PubDate = rankingNewTimestamp
				SetRankingTimestamp(item.Link, rankingNewTimestamp)
			}
			newItems[i] = item
			addedItems = append(addedItems, item)
		} else {
			// 这是已存在的条目，保持原有的 PubDate
			existingItems = append(existingItems, item)
		}
	}
	
	// 清理不再存在的条目在FilterCache和RankingTimestamps中的记录
	cleanupRankingFilterCache(cache.Items, newLinks)
	CleanupRankingTimestamps(newLinks)
	
	// 将新增条目放在前面
	result := make([]models.Item, 0, len(newItems))
	result = append(result, addedItems...)
	result = append(result, existingItems...)
	
	return result
}

// cleanupRankingFilterCache 清理榜单源中已不存在的条目的过滤缓存
func cleanupRankingFilterCache(oldItems []models.Item, newLinks map[string]bool) {
	globals.FilterCacheLock.Lock()
	defer globals.FilterCacheLock.Unlock()
	
	for _, item := range oldItems {
		if !newLinks[item.Link] {
			// 该条目不再存在于新列表中，清理其过滤缓存
			delete(globals.FilterCache, item.Link)
		}
	}
}

// GetIconForURL 从配置中获取 URL 对应的自定义图标，如果没有则自动生成 favicon
func GetIconForURL(rssURL string) string {
	// 检查 sources 配置中是否有自定义图标
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL && source.Icon != "" {
			return source.Icon
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL && feedURL.Icon != "" {
					return feedURL.Icon
				}
			}
		}
	}
	// 没有自定义图标，使用自动获取的 favicon
	return GetFaviconURL(rssURL)
}

// GetIconForFeed 获取feed的图标，优先级：1.配置的自定义图标 2.RSS的image字段 3.自动生成favicon
func GetIconForFeed(rssURL string, feed interface{}) string {
	// 1. 先检查配置中是否有自定义图标
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL && source.Icon != "" {
			return source.Icon
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL && feedURL.Icon != "" {
					return feedURL.Icon
				}
			}
		}
	}
	
	// 2. 尝试从RSS feed的image字段获取
	if feedResult, ok := feed.(*gofeed.Feed); ok {
		if feedResult.Image != nil && feedResult.Image.URL != "" {
			return feedResult.Image.URL
		}
	}
	
	// 3. 最后使用自动获取的 favicon
	return GetFaviconURL(rssURL)
}

// GetFeeds 获取feeds列表，支持文件夹
func GetFeeds() []models.Feed {
	feeds := make([]models.Feed, 0)

	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			// 文件夹类型：聚合多个源
			folderFeed := buildFolderFeed(source)
			if folderFeed != nil {
				feeds = append(feeds, *folderFeed)
			}
		} else if source.URL != "" {
			// 单个源
			globals.Lock.RLock()
			cache, ok := globals.DbMap[source.URL]
			globals.Lock.RUnlock()
			if !ok {
				log.Printf("Error getting feed from db is null %v", source.URL)
				// 返回空的Feed对象，展示卡片但内容为空
				title := "加载失败"
				if source.Name != "" {
					title = source.Name
				}
				feeds = append(feeds, models.Feed{
					Title:  title,
					Link:   source.URL,
					Icon:   source.Icon,
					Custom: map[string]string{"lastupdate": "加载失败，请稍后重试"},
					Items:  []models.Item{},
				})
				continue
			}
			// 支持自定义名称
			if source.Name != "" {
				cache.Title = source.Name
			}
			// 支持自定义图标
			if source.Icon != "" {
				cache.Icon = source.Icon
			}
			feeds = append(feeds, cache)
		}
	}

	return feeds
}

// buildFolderFeed 构建文件夹Feed，聚合多个源的内容
func buildFolderFeed(source models.FeedSource) *models.Feed {
	if !source.IsFolder() {
		return nil
	}

	folderFeed := &models.Feed{
		Title:    source.Name,
		Link:     "folder:" + source.Name,
		Icon:     source.Icon, // 文件夹的自定义图标
		IsFolder: true,
		Custom:   map[string]string{"lastupdate": "2000-01-01 00:00:00"},
		Items:    make([]models.Item, 0),
	}

	// 收集所有源的items
	for _, feedUrl := range source.Urls {
		globals.Lock.RLock()
		cache, ok := globals.DbMap[feedUrl.URL]
		globals.Lock.RUnlock()
		if !ok {
			log.Printf("Error getting feed from db is null %v", feedUrl.URL)
			// 为文件夹添加一个提示项，表明某个源加载失败
			sourceName := "未知源"
			if feedUrl.Name != "" {
				sourceName = feedUrl.Name
			}
			folderFeed.Items = append(folderFeed.Items, models.Item{
				Title:       "⚠️ " + sourceName + " 加载失败",
				Link:        feedUrl.URL,
				Description: "该订阅源暂时无法加载，请稍后重试",
				Source:      sourceName,
				PubDate:     "",
			})
			continue
		}

		// 更新文件夹的最后更新时间为最新的源更新时间
		if cache.Custom["lastupdate"] > folderFeed.Custom["lastupdate"] {
			folderFeed.Custom["lastupdate"] = cache.Custom["lastupdate"]
		}

		// 确定来源名称：优先使用自定义名称
		sourceName := cache.Title
		if feedUrl.Name != "" {
			sourceName = feedUrl.Name
		}

		// 添加items，带上来源信息
		for _, item := range cache.Items {
			newItem := item
			newItem.Source = sourceName
			folderFeed.Items = append(folderFeed.Items, newItem)
		}
	}

	// 按发布时间倒序排列
	sort.SliceStable(folderFeed.Items, func(i, j int) bool {
		return folderFeed.Items[i].PubDate > folderFeed.Items[j].PubDate
	})

	return folderFeed
}

func WatchConfigFileChanges(filePath string) {
	// 创建一个新的监控器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// 添加要监控的文件
	err = watcher.Add(filePath)
	if err != nil {
		log.Fatal(err)
	}

	// 启动一个 goroutine 来处理文件变化事件
	go func() {
		for {
			time.Sleep(30 * time.Second)
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					log.Println("通道关闭1")
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("文件已修改，重新加载配置")
					
					// 等待文件完全写入，然后重试读取配置
					var err error
					for i := 0; i < 3; i++ {
						if i > 0 {
							time.Sleep(2000 * time.Millisecond)
						}
						err = globals.ReloadConfig()
						if err == nil {
							break
						}
						log.Printf("重载配置失败（尝试 %d/3）: %v", i+1, err)
					}
					
					if err != nil {
						log.Printf("配置重载最终失败，保持使用旧配置: %v", err)
						continue // 继续监控，不退出程序
					}
					
					log.Println("配置重载成功")
					formattedTime := time.Now().Format("2006-01-02 15:04:05")

					for _, source := range globals.RssUrls.Sources {
						if source.IsFolder() {
							for _, feedUrl := range source.Urls {
								go UpdateFeed(feedUrl.URL, formattedTime)
							}
						} else if source.URL != "" {
							go UpdateFeed(source.URL, formattedTime)
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					log.Println("通道关闭2")
					return
				}
				log.Println("错误:", err)
				return
			}
		}
	}()

	select {}
}

// RefreshSingleFeed 刷新单个源或文件夹内的所有源
func RefreshSingleFeed(link string) error {
	formattedTime := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("手动刷新源: %s", link)
	
	// 查找匹配的源
	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			// 如果是文件夹，检查link是否匹配文件夹的name或任一子源
			folderMatch := false
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == link {
					folderMatch = true
					break
				}
			}
			
			// 如果匹配到文件夹中的任一源，刷新整个文件夹
			// 处理 link 格式，文件夹的 link 可能是 "folder:" + Name
			isFolderLink := link == "folder:"+source.Name
			
			if folderMatch || source.Name == link || isFolderLink {
				log.Printf("刷新文件夹: %s", source.Name)
				var wg sync.WaitGroup
				errChan := make(chan error, len(source.Urls))
				
				for _, feedUrl := range source.Urls {
					wg.Add(1)
					go func(url string) {
						defer wg.Done()
						if err := UpdateFeed(url, formattedTime); err != nil {
							errChan <- err
						}
					}(feedUrl.URL)
				}
				wg.Wait()
				close(errChan)
				
				if len(errChan) > 0 {
					return fmt.Errorf("feed update failed with errors")
				}
				return nil
			}
		} else if source.URL == link {
			// 单个源直接刷新
			log.Printf("刷新单个源: %s", link)
			return UpdateFeed(source.URL, formattedTime)
		}
	}
	
	log.Printf("未找到匹配的源: %s", link)
	return fmt.Errorf("feed not found")
}
