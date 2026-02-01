package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"rss-reader/globals"
	"rss-reader/models"
	"syscall"

	"rss-reader/utils"
	"time"

	"github.com/gorilla/websocket"
)

func init() {
	globals.Init()
	utils.InitPersistence()
}

func main() {
	// 设置优雅关闭
	go handleShutdown()
	
	go utils.UpdateFeeds()
	go utils.WatchConfigFileChanges("config.json")
	http.HandleFunc("/feeds", getFeedsHandler)
	http.HandleFunc("/ws", wsHandler)
	// http.HandleFunc("/", serveHome)
	http.HandleFunc("/", tplHandler)
	
	// 已读状态 API
	http.HandleFunc("/api/read-state", readStateHandler)
	http.HandleFunc("/api/mark-read", markReadHandler)
	http.HandleFunc("/api/mark-unread", markUnreadHandler)
	http.HandleFunc("/api/clear-read", clearReadHandler)
	http.HandleFunc("/api/refresh-feed", refreshFeedHandler)
	http.HandleFunc("/api/check-password", checkPasswordHandler)
	http.HandleFunc("/api/get-config", getConfigHandler)
	http.HandleFunc("/api/save-config", saveConfigHandler)

	//加载静态文件
	fs := http.FileServer(http.FS(globals.DirStatic))
	http.Handle("/static/", fs)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// handleShutdown 处理优雅关闭
func handleShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("收到关闭信号，正在保存数据...")
	utils.Shutdown()
	os.Exit(0)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/html; charset=utf-8")
	w.Write(globals.HtmlContent)
}

func tplHandler(w http.ResponseWriter, r *http.Request) {
	// 创建一个新的模板，并设置自定义分隔符为<< >>，避免与Vue的语法冲突
	tmplInstance := template.New("index.html").Delims("<<", ">>")
	//添加加法函数计数
	funcMap := template.FuncMap{
		"inc": func(i int) int {
			return i + 1
		},
		"json": func(v interface{}) template.JS {
			a, _ := json.Marshal(v)
			return template.JS(a)
		},
	}
	// 加载模板文件
	tmpl, err := tmplInstance.Funcs(funcMap).ParseFS(globals.DirStatic, "static/index.html")
	if err != nil {
		log.Println("模板加载错误:", err)
		return
	}

	//判断现在是否是夜间
	formattedTime := time.Now().Format("15:04:05")
	darkMode := false
	if globals.RssUrls.NightStartTime != "" && globals.RssUrls.NightEndTime != "" {
		if globals.RssUrls.NightStartTime > formattedTime || formattedTime > globals.RssUrls.NightEndTime {
			darkMode = true
		}
	}

	// 获取下次更新时间
	globals.Lock.RLock()
	nextUpdate := globals.NextUpdateTime
	globals.Lock.RUnlock()

	// 定义一个数据对象
	data := struct {
		Keywords       string
		RssDataList    []models.Feed
		DarkMode       bool
		ReFresh        int
		Groups         []string
		DefaultGroup   string
		NextUpdateTime string
	}{
		Keywords:       getKeywords(),
		RssDataList:    utils.GetFeeds(),
		DarkMode:       darkMode,
		ReFresh:        globals.RssUrls.ReFresh,
		Groups:         getGroups(utils.GetFeeds()),
		DefaultGroup:   globals.RssUrls.DefaultGroup,
		NextUpdateTime: nextUpdate.Format(time.RFC3339),
	}

	// 渲染模板并将结果写入响应
	err = tmpl.Execute(w, data)
	if err != nil {
		log.Println("模板渲染错误:", err)
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := globals.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade failed: %v", err)
		return
	}

	defer conn.Close()
	for {
		// 发送所有feeds（包括文件夹聚合的）
		feeds := utils.GetFeeds()
		for _, feed := range feeds {
			data, err := json.Marshal(feed)
			if err != nil {
				log.Printf("json marshal failure: %s", err.Error())
				continue
			}

			err = conn.WriteMessage(websocket.TextMessage, data)
			//错误直接关闭更新
			if err != nil {
				// 客户端断开连接是正常行为，不需要记录为错误
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("WebSocket unexpected close: %v", err)
				}
				return
			}
		}
		//这里原本是自动推送逻辑，现在删除，改为只发送一次或者保持连接等待（如果需要的话）
		//但是根据代码逻辑，for loop一直在跑。
		//如果删除了AutoUpdatePush，我们应该怎么处理？
		//观察原代码: sleep AutoUpdatePush minutes.
		//如果删除了，这个loop就没有sleep了? 会死循环发送。
		//所以应该把这个自动推送的loop逻辑改掉。
		//也许只需要发送一次然后hold住连接? 或者等待别的信号?
		//原逻辑是定时推送。
		//现在的需求是页面自己倒计时刷新。
		//所以WebSocket可能只需要保持连接或者作为被动通知通道(尽管目前没有实现被动通知)。
		//我们可以让它Sleep一个很长的时间，或者改为接收模式。
		//为了保持最小改动且符合"删除AutoUpdatePush功能"的需求，我们可以让它只发一次然后挂起等待客户端断开。
		select {} 
	}
}

//获取关键词也就是title
//获取feeds列表
func getKeywords() string {
	words := ""
	feeds := utils.GetFeeds()
	for _, feed := range feeds {
		if feed.Title != "" {
			words += feed.Title + ","
		}
	}
	return words
}

func getFeedsHandler(w http.ResponseWriter, r *http.Request) {
	feeds := utils.GetFeeds()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(feeds)
}

func getGroups(feeds []models.Feed) []string {
	// 优先使用配置中的 GroupOrder
	if len(globals.RssUrls.GroupOrder) > 0 {
		// 收集所有实际存在的分组
		existingGroups := make(map[string]struct{})
		for _, source := range globals.RssUrls.Sources {
			group := source.Group
			if group == "" {
				group = "关注"
			}
			existingGroups[group] = struct{}{}
		}
		
		// 按照 GroupOrder 顺序返回，只保留实际存在的分组
		result := make([]string, 0)
		for _, g := range globals.RssUrls.GroupOrder {
			if _, exists := existingGroups[g]; exists {
				result = append(result, g)
				delete(existingGroups, g)
			}
		}
		// 添加未在 GroupOrder 中的分组
		for g := range existingGroups {
			result = append(result, g)
		}
		if len(result) == 0 {
			result = append(result, "关注")
		}
		return result
	}
	
	// 使用配置中订阅源的分组顺序
	groupSet := make(map[string]struct{})
	groups := make([]string, 0)
	
	// 从配置中获取分组顺序
	for _, source := range globals.RssUrls.Sources {
		group := source.Group
		if group == "" {
			group = "关注"
		}
		if _, exists := groupSet[group]; !exists {
			groupSet[group] = struct{}{}
			groups = append(groups, group)
		}
	}
	
	// 确保有 feeds 数据时也检查（兼容性）
	for _, feed := range feeds {
		group := feed.Group
		if group == "" {
			group = "关注"
		}
		if _, exists := groupSet[group]; !exists {
			groupSet[group] = struct{}{}
			groups = append(groups, group)
		}
	}
	
	// 如果没有任何分组，返回默认的"关注"
	if len(groups) == 0 {
		groups = append(groups, "关注")
	}
	
	return groups
}

// readStateHandler 获取已读状态
func readStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	readState := utils.GetReadState()
	
	// 只返回链接列表，不返回时间戳（减少数据量）
	links := make([]string, 0, len(readState))
	for link := range readState {
		links = append(links, link)
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(links)
}

// markReadHandler 标记文章为已读
func markReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		Links []string `json:"links"`
		Link  string   `json:"link"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	// 支持单个或批量标记
	if len(req.Links) > 0 {
		utils.MarkReadBatch(req.Links)
	} else if req.Link != "" {
		utils.MarkRead(req.Link)
	} else {
		http.Error(w, "Missing link or links", http.StatusBadRequest)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// markUnreadHandler 标记文章为未读
func markUnreadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		Link string `json:"link"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	if req.Link == "" {
		http.Error(w, "Missing link", http.StatusBadRequest)
		return
	}
	
	utils.MarkUnread(req.Link)
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// clearReadHandler 清除所有已读状态
func clearReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	utils.ClearAllReadState()
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// refreshFeedHandler 刷新单个源
func refreshFeedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		Link string `json:"link"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	if req.Link == "" {
		http.Error(w, "Missing link", http.StatusBadRequest)
		return
	}
	
	// 触发立即更新指定的源
	if err := utils.RefreshSingleFeed(req.Link); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// checkPasswordHandler 验证密码
func checkPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if globals.RssUrls.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
		return
	}

	if req.Password == globals.RssUrls.Password {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false, "message":"Password incorrect"}`))
	}
}

// getConfigHandler 获取当前配置
func getConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		Password string `json:"password"`
	}
	
	if globals.RssUrls.Password != "" {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Password != globals.RssUrls.Password {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globals.RssUrls)
}

// saveConfigHandler 保存配置
func saveConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string        `json:"password"`
		Config   models.Config `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 验证旧密码
	if globals.RssUrls.Password != "" && req.Password != globals.RssUrls.Password {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := utils.SaveConfig(req.Config); err != nil {
		log.Printf("Save config failed: %v", err)
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}
