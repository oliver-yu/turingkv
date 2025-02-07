package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jessevdk/go-flags"
	"github.com/lestrrat/go-file-rotatelogs"
	log "github.com/sirupsen/logrus"
	"github.com/turingkv/raft-kv-proxy/src/hash"
	"github.com/turingkv/raft-kv/src/utils"
	"golang.org/x/net/context"
    "google.golang.org/grpc"
    pb "github.com/turingkv/kvrpc"
    "math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

const MOD = 2048

type Opts struct {
	ApiPort    string `long:"api_port" env:"API_PORT" default:":8080" description:":port for a api port"`
	GroupCount int    `long:"group_count" env:"GROUP_COUNT" default:"0" description:"Raft Group Count"`
	ZkAddress  string `long:"zk_address" env:"ZK_ADDRESS" default:"127.0.0.1:2181" description:"zkServerAddress"`
	LogPath    string `long:"log_path" env:"LOG_PATH" default:"logs/turing-kv.log" description:"logPath"`
}

type setKeyData struct {
	Value string `json:"value"`
}

func getKeyView(opts Opts, client *utils.SdClient) func(*gin.Context) {

	view := func(c *gin.Context) {
		//通过key计算其所在的raft组
		key := c.Param("key")
		slot := hash.Crc32IEEE([]byte(key), MOD)
		fmt.Println("SLOT", slot)

		base := uint32(MOD / opts.GroupCount)
		groupId := slot / base

		//从对应raft组中的服务器中读出数据
		serverNodes, err := client.GetNodes("group_" + strconv.Itoa(int(groupId)))
		randServerID := rand.Intn(len(serverNodes))
	    
        conn, err := grpc.Dial(fmt.Sprintf("%s:%d", serverNodes[randServerID].Host, serverNodes[randServerID].Port), grpc.WithInsecure())
        if err != nil {
            log.Errorf("did connect: %v", err)
        }
        defer conn.Close()

        c_rpc := pb.NewApiClient(conn)
        r, err := c_rpc.GetV(context.Background(), &pb.VRequest{Key: key})
        if err != nil {
            log.Errorf("post v error: %v", err)
        }
        
        result := r.Value
        log.Infof("get k %s v %s from  server %s port %d", key, result, serverNodes[randServerID].Host, serverNodes[randServerID].Port)
		if err == nil {
			c.JSON(200, gin.H{
				"value": result,
			})
		}

	}
	return view
}

func setKeyView(opts Opts, client *utils.SdClient) func(*gin.Context) {

	view := func(c *gin.Context) {

		//通过key计算其应该写入的raft组
		key := c.Param("key")
		slot := hash.Crc32IEEE([]byte(key), MOD)
		fmt.Println("SLOT", slot)
		base := uint32(MOD / opts.GroupCount)
		groupId := slot / base

        /*
			存储数据到具体group
		*/

		// 拿到对应group的server列表
		serverNodes, err := client.GetNodes("group_" + strconv.Itoa(int(groupId)))
		randServerID := rand.Intn(len(serverNodes))

        conn, err := grpc.Dial(fmt.Sprintf("%s:%d", serverNodes[randServerID].Host, serverNodes[randServerID].Port), grpc.WithInsecure())
        if err != nil {
            log.Errorf("did connect: %v", err)
        }

        defer conn.Close()
        c_rpc := pb.NewApiClient(conn)
        
        var data setKeyData
		err = c.BindJSON(&data)
		if err != nil {
			panic(err)
		}
        
        r_ , err := c_rpc.PostKV(context.Background(), &pb.KVRequest{Key:key, Value: data.Value})
        if err != nil {
            log.Errorf("post kv error: %v", err)
        }
          
        if r_ != nil {
		    log.Infof("post k %s v %s to  server %s port %d is %v", key, data.Value, serverNodes[randServerID].Host, serverNodes[randServerID].Port, r_.Isok)
        }

		if err == nil {
			c.JSON(200, gin.H{
				"value": data.Value,
			})
		}

	}
	return view
}

func main() {

	// 解析传入参数
	var opts Opts
	p := flags.NewParser(&opts, flags.Default)
	if _, err := p.ParseArgs(os.Args[1:]); err != nil {
		log.Panicln(err)
	}

	//设置日志
	if logf, err := rotatelogs.New(
		opts.LogPath+".%Y%m%d",
		rotatelogs.WithMaxAge(time.Hour*24*30),
		rotatelogs.WithRotationTime(time.Hour*24),
	); err != nil {
		log.WithError(err).Error("create rotatelogs, use default io.writer instead")
	} else {
		log.SetOutput(logf)
	}

	router := gin.Default()

	router.GET("/", func(context *gin.Context) {
		context.JSON(200, gin.H{
			"message": "pong",
		})
	})

	//连接 zk
	zkServers := strings.Split(opts.ZkAddress, ",")
	client, err := utils.NewClient(zkServers, "/api", 1)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	router.POST("/keys/:key/", setKeyView(opts, client))
	router.GET("/keys/:key/", getKeyView(opts, client))

	router.Run(opts.ApiPort)
	log.Info("Start Server Listen on port: %s", opts.ApiPort)
}
