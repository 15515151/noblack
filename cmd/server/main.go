package main

// 敏感词检测服务入口。
//
// 运行:
//   go run ./cmd/server -words ./words.json -addr :8080
//
// 启动流程:
//   1. 加载词库文件, 构建初始自动机。
//   2. 将其放入 Store (atomic.Value 持有)。
//   3. 启动 fsnotify 监听, 实现文件变更自动热加载。
//   4. 注册 HTTP 路由并启动服务。
//   5. 监听系统信号, 优雅关闭。

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"noblack/internal/api"
	"noblack/internal/matcher"
	"noblack/internal/stats"
	"noblack/internal/store"
)

func main() {
	var (
		wordsPath = flag.String("words", "./words.json", "敏感词库文件路径 (JSON)")
		addr      = flag.String("addr", ":8080", "HTTP 监听地址")
		watch     = flag.Bool("watch", true, "是否启用 fsnotify 文件监听热加载")
		caseIns   = flag.Bool("ci", false, "匹配是否大小写不敏感 (主要影响英文词, 如 Bilibili≈bilibili)")
		defLevel  = flag.String("default-level", "Low", "词条未标注 level/levels 时使用的默认等级")
		statsFile = flag.String("stats-file", "", "统计持久化文件路径 (JSON); 留空则不持久化, 重启后统计归零")
		statsIvl  = flag.Duration("stats-flush-interval", 30*time.Second, "统计后台落盘间隔 (仅在 -stats-file 非空时生效)")
		token     = flag.String("token", "", "词条写操作(新增/修改/删除)的鉴权令牌; 留空则不鉴权")
	)
	flag.Parse()

	// 并行度诊断: GOMAXPROCS 决定 Go 能同时用几个核。
	log.Printf("并行度: GOMAXPROCS=%d, NumCPU=%d", runtime.GOMAXPROCS(0), runtime.NumCPU())

	opts := matcher.Options{
		CaseInsensitive: *caseIns,
		DefaultLevel:    *defLevel,
	}

	// 1. 加载词条。
	entries, err := matcher.LoadEntries(*wordsPath, opts)
	if err != nil {
		log.Fatalf("初始化词库失败: %v", err)
	}

	// 2. 放入 Store (内部构建自动机)。
	st := store.New(*wordsPath, entries, opts)
	log.Printf("初始化完成, 加载词条数: %d, 等级集合: %v", st.Current().Size(), st.Current().Levels())

	// 3. 统计收集器 (可选持久化)。
	metrics := stats.New()
	done := make(chan struct{})
	var persister *stats.Persister
	if *statsFile != "" {
		persister = stats.NewPersister(metrics, *statsFile, *statsIvl)
		if err := persister.LoadInto(); err != nil {
			// 恢复失败不致命: 从空统计开始, 仅告警。
			log.Printf("[stats] 恢复历史统计失败, 将从零开始: %v", err)
		} else {
			s := metrics.Snapshot(0)
			log.Printf("[stats] 已启用持久化: %s (间隔 %s), 恢复 check_requests=%d", *statsFile, *statsIvl, s.CheckRequests)
		}
		go persister.Run(done) // 后台定期落盘
	}

	// 4. 启动文件监听 (可选)。
	if *watch {
		go func() {
			if err := st.Watch(done); err != nil {
				log.Printf("文件监听启动失败 (仅影响自动热加载, 不影响 /reload): %v", err)
			}
		}()
	}

	// 5. 注册路由 (含前端静态页与 API)。
	mux := http.NewServeMux()
	api.NewHandler(st, metrics, *token).Register(mux)
	if *token != "" {
		log.Printf("已启用词条写操作鉴权 (新增/修改/删除需令牌)")
	}

	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 5. 异步启动 + 信号优雅关闭。
	go func() {
		log.Printf("敏感词检测服务已启动, 监听 %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	// os.Interrupt (Ctrl+C) 跨平台可捕获; SIGTERM 在类 Unix 下由编排器 (k8s/systemd) 发送。
	// 注意: Windows 不投递可捕获的 SIGTERM, 优雅关闭主要靠 Ctrl+C; 统计不丢则靠后台定期落盘。
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Printf("收到关闭信号, 正在优雅关闭...")

	close(done) // 停止文件监听与统计后台循环
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("优雅关闭超时: %v", err)
	}
	// 确定性地做最后一次落盘 (Run 内的退出刷盘是异步的, 这里再同步刷一次保证不丢)。
	if persister != nil {
		if err := persister.Flush(); err != nil {
			log.Printf("[stats] 关闭时落盘失败: %v", err)
		}
	}
	log.Printf("服务已关闭")
}
