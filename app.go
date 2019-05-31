package yago

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron"
	"google.golang.org/grpc"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type App struct {
	// 是否开启debug模式
	DebugMode bool
	// http web 引擎
	httpEngine *gin.Engine
	// http run mode
	HttpRunMode string
	// 开启http服务
	HttpEnable bool
	// http路由配置
	HttpRouters []*HttpRouter
	// http close chan
	httpCloseChan     chan int
	httpCloseDoneChan chan int

	// 开启task服务
	TaskEnable bool
	// task路由配置
	TaskRouters []*TaskRouter
	// task close chan
	taskCloseChan     chan int
	taskCloseDoneChan chan int

	// rpc
	RpcEnable bool
	rpcEngine *grpc.Server

	// rpc close chan
	rpcCloseChan     chan int
	rpcCloseDoneChan chan int
}

var (
	errHttpRouteEmpty = errors.New("http router is empty")
	errTaskRouteEmpty = errors.New("task router is empty")
)

func NewApp() *App {
	// new app
	app := new(App)

	app.DebugMode = Config.GetBool("app.debug")

	// init http
	app.HttpEnable = Config.GetBool("app.http_enable")
	if app.HttpEnable {
		if app.DebugMode == true {
			app.HttpRunMode = gin.DebugMode
		} else {
			app.HttpRunMode = gin.ReleaseMode
		}
		gin.SetMode(app.HttpRunMode)
		app.httpEngine = gin.New()
		// use logger
		app.httpEngine.Use(gin.Logger())
		app.httpCloseChan = make(chan int, 1)
		app.httpCloseDoneChan = make(chan int, 1)
	}

	// init rpc
	app.RpcEnable = Config.GetBool("app.rpc_enable")
	if app.RpcEnable {
		app.rpcCloseChan = make(chan int, 1)
		app.rpcCloseDoneChan = make(chan int, 1)
	}

	// init task
	app.TaskEnable = Config.GetBool("app.task_enable")
	if app.TaskEnable {
		app.taskCloseChan = make(chan int, 1)
		app.taskCloseDoneChan = make(chan int, 1)
	}

	return app
}

func init() {
	log.SetFlags(log.LstdFlags)
}

func (a *App) Run() {
	if a.TaskEnable {
		// 开启 task
		go a.runTask()
	}

	if a.RpcEnable {
		// 开启 rpc 服务
		go a.runRpc()
	}

	if a.HttpEnable {
		// 开启 http
		go a.runHttp()
	}

	a.startSignal()
}

func (a *App) loadHttpRouter() error {
	if len(HttpRouterMap) == 0 {
		return errHttpRouteEmpty
	}

	a.httpEngine.Use(func(c *gin.Context) {
		req := c.Request
		query := req.URL.Query()
		for k, v := range query {
			c.Set(k, v[0])
		}

		err := req.ParseForm()
		if err != nil {
			return
		}
		for k, v := range req.PostForm {
			c.Set(k, v[0])
		}
	})

	for _, r := range HttpRouterMap {
		method := strings.ToUpper(r.Method)
		action := r.Action
		handler := func(c *gin.Context) {
			if ctx, err := NewCtx(c); err == nil {
				action(ctx)
			} else {
				if ctx != nil {
					ctx.SetError(ErrParam, err.Error())
				}
			}
		}
		log.Println("[HTTP]", r.Url, runtime.FuncForPC(reflect.ValueOf(action).Pointer()).Name())
		switch method {
		case http.MethodGet:
			a.httpEngine.GET(r.Url, handler)
		case http.MethodPost:
			a.httpEngine.POST(r.Url, handler)
		case http.MethodDelete:
			a.httpEngine.DELETE(r.Url, handler)
		case http.MethodPut:
			a.httpEngine.PUT(r.Url, handler)
		case http.MethodOptions:
			a.httpEngine.OPTIONS(r.Url, handler)
		case http.MethodPatch:
			a.httpEngine.PATCH(r.Url, handler)
		case http.MethodHead:
			a.httpEngine.HEAD(r.Url, handler)
		default:
			a.httpEngine.Any(r.Url, handler)
		}
	}
	return nil
}

func (a *App) runHttp() {
	// load router
	if err := a.loadHttpRouter(); err != nil {
		a.httpCloseDoneChan <- 1
		return
	}
	// listen and serve
	srv := &http.Server{
		Addr:    Config.GetString("app.http_addr"),
		Handler: a.httpEngine,
	}

	go func() {
		// service connections
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	select {
	case <-a.httpCloseChan:
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(Config.GetInt64("app.http_stop_time_wait"))*time.Second,
		)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			// log
		}
		a.httpCloseDoneChan <- 1
	}
}

func (a *App) loadTaskRouter() error {
	if len(TaskRouterList) == 0 {
		return errTaskRouteEmpty
	}

	return nil
}

func (a *App) runTask() {
	if err := a.loadTaskRouter(); err != nil {
		a.taskCloseDoneChan <- 1
		return
	}

	c := cron.New()
	wg := sync.WaitGroup{}
	for _, router := range TaskRouterList {
		if !Config.GetBool("is_task_host") {
			continue
		}
		action := router.Action
		if router.Spec == "@loop" {
			go func() {
				wg.Add(1)
				action()
				log.Println("[TASK]", "stop", runtime.FuncForPC(reflect.ValueOf(action).Pointer()).Name())
				wg.Done()
			}()
		} else {
			err := c.AddFunc(router.Spec, func() {
				wg.Add(1)
				action()
				wg.Done()
			})
			if err != nil {
				continue
			}
		}
		log.Println("[TASK]", router.Spec, runtime.FuncForPC(reflect.ValueOf(router.Action).Pointer()).Name())
	}

	c.Start()

	select {
	case <-a.taskCloseChan:
		go func() {
			c.Stop()
			wg.Wait()
			a.taskCloseDoneChan <- 1
		}()
		time.Sleep(time.Duration(Config.GetInt64("app.task_stop_time_wait")) * time.Second)
		a.taskCloseDoneChan <- 1
	}
}

var RpcServer = grpc.NewServer()

// rpc
func (a *App) runRpc() {
	rpcAddr := Config.GetString("app.rpc_addr")
	lis, err := net.Listen("tcp", rpcAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	a.rpcEngine = RpcServer

	for k, v := range a.rpcEngine.GetServiceInfo() {
		for _, method := range v.Methods {
			log.Println("[GRPC]", k, method.Name)
		}
	}

	go func() {
		if err := a.rpcEngine.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	select {
	case <-a.rpcCloseChan:
		a.rpcEngine.GracefulStop()
		a.rpcCloseDoneChan <- 1
	}
}

var TaskCloseChan = make(chan int)

var GlobalCloseChan = make(chan int)

func (a *App) startSignal() {
	pid := os.Getpid()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	for {
		s := <-signals
		log.Println("recv", s)

		switch s {
		case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
			log.Println("Graceful Shutdown...")
			if a.TaskEnable {
				close(TaskCloseChan)
				a.taskCloseChan <- 1
				<-a.taskCloseDoneChan
				log.Println("Task Server Stop OK")
			}
			if a.HttpEnable {
				a.httpCloseChan <- 1
				<-a.httpCloseDoneChan
				log.Println("Http Server Stop OK")
			}

			if a.RpcEnable {
				a.rpcCloseChan <- 1
				<-a.rpcCloseDoneChan
				log.Println("Rpc Server Stop OK")
			}

			// broad close chan
			close(GlobalCloseChan)

			log.Println("Process", pid, "Exit OK")
			os.Exit(0)
		case syscall.SIGUSR2:
			log.Println("Restart...")
			log.Println("Process", pid, "Restart ok")
		}
	}
}