package main

import (
	"cli"
	"config"
	"errors"
	"flag"
	"fmt"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	pili2 "github.com/pili-engineering/pili-sdk-go.v2/pili"
	"github.com/qiniu/log"
	"model"
	"os"
	"runtime"
	"time"
	"util"
)

const (
	VERSION = "1.0"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	var confFile string
	flag.StringVar(&confFile, "c", "", "config file for the service")

	flag.Usage = func() {
		fmt.Println(`
Usage of qasync:
    -c="": config file for the service

version ` + VERSION)
	}

	flag.Parse()

	if confFile == "" {
		fmt.Println("Err: no config file specified")
		os.Exit(1)
	}

	_, statErr := os.Stat(confFile)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			fmt.Println("Err: config file not found")
		} else {
			fmt.Println(statErr)
		}
		os.Exit(1)
	}

	//load config
	cfg, cfgErr := config.LoadConfig(confFile)
	if cfgErr != nil {
		fmt.Println(cfgErr)
		os.Exit(1)
	}

	//init log
	lErr := initLog(cfg.App.QLogLevel, cfg.App.LogFile)
	if lErr != nil {
		fmt.Println("init log error,", lErr)
		os.Exit(1)
	}

	//init orm
	ormErr := cli.InitOrm(&cfg.Orm)
	if ormErr != nil {
		fmt.Println(ormErr)
		os.Exit(1)
	}

	mac := &pili2.MAC{cfg.App.AccessKey, []byte(cfg.App.SecretKey)}
	client := pili2.New(mac, nil)
	hub := client.Hub(cfg.App.Hub)

	startTimer(mac)

	router := gin.Default()
	router.Static("/assets", "./assets")
	router.LoadHTMLGlob("templates/*")

	//************************** monitor ******************************//
	router.GET("/pili/v1/server", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"code":    200,
			"message": "success",
		})
	})

	//************************** User **********************************
	//create user
	router.POST("/pili/v1/user/new", func(c *gin.Context) {
		var reqInfo cli.Users
		rErr := c.BindJSON(&reqInfo)
		if rErr != nil {
			log.Errorf("request paramter error,%s\n", rErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "the request's paramter error",
			})
			c.Abort()
			return
		}
		//check paramters
		if reqInfo.Name == "" || reqInfo.Password == "" {
			log.Errorf("name or password is null,name=%s,password=%s\n", reqInfo.Name, reqInfo.Password)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name or password is null",
			})
			c.Abort()
			return
		}
		if reqInfo.Room == "" {
			log.Errorf("room is null")
			c.JSON(400, gin.H{
				"code":    400,
				"message": "room is null",
			})
			c.Abort()
			return
		}
		reqInfo.Deadline = time.Now().Unix()
		//create room
		_, nrErr := cli.RoomCreate(mac, reqInfo.Room, reqInfo.Name, 99)
		if nrErr != nil {
			log.Errorf("create room error,%s\n", nrErr)
			c.JSON(611, gin.H{
				"code":    611,
				"message": fmt.Sprintf("%s\n", nrErr),
			})
			c.Abort()
			return
		}
		iErr := cli.InsertUser(&reqInfo)
		if iErr != nil {
			log.Errorf("create user %s error,%s\n", reqInfo.Name, iErr)
			//delete room
			cli.RoomDelete(mac, reqInfo.Room)
			c.JSON(400, gin.H{
				"code":    400,
				"message": fmt.Sprintf("%s\n", iErr),
			})
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code": 200,
			"name": reqInfo.Name,
			"room": reqInfo.Room,
		})
	})

	//query user
	router.GET("/pili/v1/user/query/:name", func(c *gin.Context) {
		name := c.Param("name")
		if name == "" {
			log.Errorf("user name is null")
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name is null",
			})
			c.Abort()
			return
		}
		u, uErr := cli.UserIsExisted(name)
		if uErr != nil {
			log.Errorf("query user failed, %s\n", name)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "user not found",
			})
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code": 200,
			"name": u.Name,
			"room": u.Room,
		})
		return
	})

	//update user
	router.POST("/pili/v1/user/update/:name", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		author, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": authErr.Error(),
			})
			c.Abort()
			return
		}
		//get request paramter
		name := c.Param("name")
		if name == "" {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name is null",
			})
			c.Abort()
			return
		}
		if name != author {
			fmt.Printf("name=%s,author=%s\n", name, author)
			c.JSON(403, gin.H{
				"code":    403,
				"message": "name and authentication are inconsistent",
			})
			c.Abort()
			return
		}
		var reqInfo model.ReqUpdateUser
		rErr := c.BindJSON(&reqInfo)
		if rErr != nil {
			log.Errorf("the body paramter error, %s\n", rErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "the body error",
			})
			c.Abort()
			return
		}
		udErr := cli.UpdateUser(name, reqInfo.Password)
		if udErr != nil {
			log.Errorf("update user's info failed, %s\n", udErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "update user's info failed",
			})
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code":    200,
			"message": "ok",
		})
		return
	})

	//delete user
	router.POST("/pili/v1/user/delete/:name", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		author, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": "no auhtorized",
			})
			c.Abort()
			return
		}
		//get request paramter
		name := c.Param("name")
		if name == "" {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name is null",
			})
			c.Abort()
			return
		}
		if name != author {
			fmt.Printf("name=%s,author=%s\n", name, author)
			c.JSON(403, gin.H{
				"code":    403,
				"message": "name and authentication are inconsistent",
			})
			c.Abort()
			return
		}
		//删除用户
		_, retErr := cli.DeleteUser(mac, name)
		if retErr != nil {
			log.Errorf("delete user error,%s\n", retErr)
			c.JSON(400, retErr)
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code":    200,
			"message": "ok",
		})
		return
	})

	//login
	router.POST("/pili/v1/login", func(c *gin.Context) {
		var reqInfo model.ReqLoginBody
		rErr := c.BindJSON(&reqInfo)
		if rErr != nil {
			log.Errorf("request paramter error,%s\n", rErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "the request's paramter error",
			})
			c.Abort()
			return
		}
		if reqInfo.Name == "" || reqInfo.Password == "" {
			log.Errorf("name or password is null")
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name or password is null",
			})
			c.Abort()
			return
		}
		err := cli.QueryUser(reqInfo.Name, reqInfo.Password)
		if err != nil {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "name or password is wrong",
			})
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code":    200,
			"message": "ok",
		})
		return
	})

	//**************************** Room ****************************
	//query room
	router.GET("/pili/v1/room/query/:id", func(c *gin.Context) {
		//get request paramter
		roomid := c.Param("id")
		if roomid == "" {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "roomid is null",
			})
			c.Abort()
			return
		}
		r, err := cli.RoomStatus(mac, roomid)
		if err != nil {
			c.JSON(400, err)
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code":    200,
			"room":    r.Room,
			"ownerId": r.OwnerUserID,
			"userMax": r.UserMax,
			"status":  r.Status,
		})
	})

	//create room
	router.POST("/pili/v1/room/new", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		_, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": authErr.Error(),
			})
			c.Abort()
			return
		}
		var reqInfo model.ReqNewRoomBody
		rErr := c.BindJSON(&reqInfo)
		if rErr != nil {
			log.Errorf("request paramter error,%s\n", rErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "the request's paramter error",
			})
			c.Abort()
			return
		}
		if reqInfo.Room == "" || reqInfo.User == "" {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "room or user is null",
			})
			c.Abort()
			return
		}
		if reqInfo.Max <= 0 {
			reqInfo.Max = 99
		}
		ret, retErr := cli.RoomCreate(mac, reqInfo.Room, reqInfo.User, reqInfo.Max)
		if retErr != nil {
			c.JSON(400, retErr)
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code": 200,
			"room": ret.Room,
		})
	})

	//delete room
	router.POST("/pili/v1/room/delete/:id", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		author, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": authErr.Error(),
			})
			c.Abort()
			return
		}

		roomid := c.Param("id")
		//query room status
		r, err := cli.RoomStatus(mac, roomid)
		if err != nil {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "delete user failed",
			})
			c.Abort()
			return
		}
		if author != r.OwnerUserID {
			c.JSON(403, gin.H{
				"code":    403,
				"message": "no authorized",
			})
			c.Abort()
			return
		}
		_, retErr := cli.RoomDelete(mac, roomid)
		if retErr != nil {
			c.JSON(400, gin.H{
				"code":    400,
				"message": "delete room failed",
			})
			c.Abort()
			return
		}
		c.JSON(200, gin.H{
			"code":    200,
			"message": "ok",
		})
		return
	})

	//new roomtoken
	router.POST("/pili/v1/room/token", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		_, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": authErr.Error(),
			})
			c.Abort()
			return
		}

		var reqInfo model.ReqNewRoomTokenBody
		rErr := c.BindJSON(&reqInfo)
		if rErr != nil {
			log.Errorf("request paramter error,%s\n", rErr)
			c.JSON(400, gin.H{
				"code":    400,
				"message": "the request's paramter error",
			})
			c.Abort()
			return
		}
		roomtoken := cli.CreateToken(mac, reqInfo.Room, reqInfo.User, reqInfo.Version)
		c.String(200, roomtoken)
	})

	//************************* Stream *******************************//
	//create stream
	router.POST("/pili/v1/stream/:id", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		_, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": "no authorized",
			})
			c.Abort()
			return
		}

		id := c.Params.ByName("id")
		//先创建流
		hub.Create(id)
		url := pili2.RTMPPublishURL("pili-publish.ps.pdex-service.com", cfg.App.Hub, id, mac, 3600)
		c.JSON(200, gin.H{
			"code": 200,
			"url":  url,
		})
		return
	})

	//query live url
	router.GET("/pili/v1/stream/query/:id", func(c *gin.Context) {
		//check authorization
		token := c.Request.Header.Get("Authorization")
		_, authErr := authorization(token)
		if authErr != nil {
			log.Errorf("%s\n", authErr)
			c.JSON(403, gin.H{
				"code":    403,
				"message": authErr.Error(),
			})
			c.Abort()
			return
		}

		key := c.Params.ByName("id")
		rtmpurl := pili2.RTMPPlayURL("pili-live-rtmp.ps.pdex-service.com", cfg.App.Hub, key)
		hlsurl := pili2.HLSPlayURL("pili-live-hdl.ps.pdex-service.com", cfg.App.Hub, key)
		hdlurl := pili2.HDLPlayURL("pili-live-hls.ps.pdex-service.com", cfg.App.Hub, key)
		c.JSON(200, gin.H{
			"code": 200,
			"rtmp": rtmpurl,
			"hdl":  hdlurl,
			"hls":  hlsurl,
		})
		return
	})

	router.Run(fmt.Sprintf(":%d", cfg.Server.ListenPort))
}

func authorization(token string) (string, error) {
	if token == "" {
		return "", errors.New("authorization is null")
	}
	return util.UnderOfAuthority(token)
}

//定时删除
func startTimer(mac *pili2.MAC) {
	go func() {
		for {
			ttl := time.Now().Unix() - (30 * 24 * 60 * 60) //second
			cli.DeleteUserByTimer(mac, ttl)
			now := time.Now()
			// 计算下一个零点
			next := now.Add(time.Hour * 24)
			next = time.Date(next.Year(), next.Month(), next.Day(), 23, 59, 59, 59, next.Location())
			t := time.NewTimer(next.Sub(now))
			<-t.C
		}
	}()
}

func initLog(logLevel int, logFile string) (err error) {
	log.Info("init log")
	log.SetOutputLevel(logLevel)

	logFp, openErr := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if openErr != nil {
		err = openErr
		return
	}

	log.SetOutput(logFp)

	return
}
