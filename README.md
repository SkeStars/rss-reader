# 简述

实时展示rss订阅最新消息。

## 特性

- 打包后镜像大小仅有约20MB，通过docker实现一键部署

- 支持自定义配置页面数据自动刷新

- 响应式布局，能够兼容不同的屏幕大小

- 良好的SEO，首次加载使用模版引擎快速展示页面内容

- 支持添加多个RSS订阅链接

- 简洁的页面布局，可以查看每个订阅链接最后更新时间

- 支持夜间模式

- config.json配置文件支持热更新

2023年7月28日，进行了界面改版和升级

![](pc.png)

![](mobile.png)

# 配置文件

配置文件位于config.json，sources是RSS订阅链接，示例如下

```json
{
    "values": [
        "https://www.zhihu.com/rss",
        "https://tech.meituan.com/feed/",
        "http://www.ruanyifeng.com/blog/atom.xml",
        "https://feeds.appinn.com/appinns/",
        "https://v2ex.com/feed/tab/tech.xml",
        "https://www.cmooc.com/feed",
        "http://www.sciencenet.cn/xml/blog.aspx?di=30",
        "https://www.douban.com/feed/review/book",
        "https://www.douban.com/feed/review/movie",
        "https://www.geekpark.net/rss",
        "https://hostloc.com/forum.php?mod=rss&fid=45&auth=389ec3vtQanmEuRoghE%2FpZPWnYCPmvwWgSa7RsfjbQ%2BJpA%2F6y6eHAx%2FKqtmPOg"
    ],
    "schedules": [
        {"startTime": "08:00:00", "endTime": "23:00:00", "refresh": 45},
        {"startTime": "23:00:00", "endTime": "08:00:00", "refresh": 0}
    ],
    "nightStartTime": "06:30:00",
    "nightEndTime": "19:30:00"
}
```

名称 | 说明
-|-
values | rss订阅链接（必填）
schedules | 抓取计划规则数组。定义不同时间段的刷新频率（非必填）
nightStartTime | 日间开始时间 ，如 06:30:00
nightEndTime | 日间结束时间，如 19:30:00

## 抓取计划规则

schedules 数组中的每条规则支持以下字段：

字段 | 说明
-|-
startTime | 开始时间，格式 HH:mm:ss（必填）
endTime | 结束时间，格式 HH:mm:ss（必填）
refresh | 刷新频率，单位分钟。设为 0 表示该时段暂停抓取

**优先级**：规则按顺序从上往下匹配，第一个匹配当前时间的规则生效。如果当前时间不匹配任何规则，将不会进行自动刷新。

## 榜单模式

对于榜单类型的RSS源（如热榜、排行榜等），这类源通常没有发布时间，条目会根据排名变化。启用榜单模式后，每次更新源时会智能识别新增条目，并将其放在列表前面，方便用户发现新上榜内容。

配置方式：在源配置中添加 `rankingMode: true`

```json
{
    "sources": [
        {
            "url": "https://example.com/ranking.rss",
            "name": "热榜",
            "rankingMode": true
        },
        {
            "name": "聚合榜单",
            "urls": [
                {"url": "https://example.com/hot.rss", "name": "热点榜", "rankingMode": true},
                {"url": "https://example.com/new.rss", "name": "新闻榜", "rankingMode": true}
            ]
        }
    ]
}
```

榜单模式特性：
- **新增条目自动置顶**：每次刷新时，新出现在榜单上的条目会显示在列表最前面
- **智能缓存清理**：已从榜单移除的条目，其相关缓存（包括AI过滤缓存）会自动清理
- **时间戳处理**：新增条目会使用更新时的当前时间作为 pubDate，在文件夹聚合时按更新时间排序
- **适用场景**：适合热榜、排行榜、趋势榜等动态变化的RSS源

# 使用方式

## Docker部署

环境要求：Git、Docker、Docker-Compose

克隆项目

```bash
git clone https://github.com/srcrs/rss-reader
```

进入rss-reader文件夹，运行项目

```bash
docker-compose up -d
```

# 开发流程优化

为了解决修改代码后频繁构建镜像耗时过长的问题，我们引入了以下优化方案：

### 1. Docker 构建优化
已经优化了 `Dockerfile` 的分层结构。现在 `go mod download` 会在拷贝源码之前运行并被缓存。**除非你修改了 `go.mod` 或 `go.sum`，否则重新构建时不会重复下载依赖。**

同时默认开启了 `GOPROXY=https://goproxy.cn,direct`，加速国内依赖下载。

### 2. 热重载开发模式（推荐）
我们集成了 [Air](https://github.com/air-verse/air) 工具，支持在 Docker 容器内实现代码修改后自动重载，无需重启或重新构建容器。

**使用方法：**
1. 确保已安装 Docker 和 Docker-Compose。
2. 在项目根目录下运行：
   ```bash
   docker-compose -f docker-compose.dev.yml up
   ```
3. 现在你可以直接修改 `.go` 或 `.html` 文件，容器会自动检测改动并瞬间重新编译运行。
4. 开发环境默认监听 `8081` 端口（可在 `docker-compose.dev.yml` 中修改）。

部署成功后，通过ip+端口号访问

# nginx反代

这里需要注意/ws，若不设置proxy_read_timeout参数，则默认1分钟断开。静态文件增加gzip可以大幅压缩网络传输数据

```conf
server {
    listen 443 ssl;
    server_name rss.lass.cc;
    ssl_certificate  fullchain.cer;
    ssl_certificate_key lass.cc.key;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml application/xml+rss text/javascript;
    location / {
        proxy_pass  http://localhost:8080;
    }
    location /ws {
        proxy_pass http://localhost:8080/ws;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 300s;
    }
}

server {
    listen 80;
    server_name rss.lass.cc;
    rewrite ^(.*)$ https://$host$1 permanent;
}
```
