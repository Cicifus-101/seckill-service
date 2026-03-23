package main

import (
	"flag"
	"fmt"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"os"
	"path/filepath"
	"seckill-service/internal/conf"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"gorm.io/gen"
)

// gen生成代码配置

var flagconf string

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

type DBConfig struct {
	Name     string
	Source   string
	Tables   []string
	OutPath  string
	ModelPkg string
}

func connectDB(source string) *gorm.DB {
	db, err := gorm.Open(mysql.Open(source), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
	})
	if err != nil {
		log.Fatalf("connect db err: %v", err)
	}
	return db
}

func main() {
	flag.Parse() //解析配置

	c := config.New(
		config.WithSource(
			file.NewSource(flagconf),
		),
	)
	defer c.Close()

	// 将配置加载到配置管理器的内存中
	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	// 创建基础目录
	baseDir := "internal/data"

	// 定义四个数据库的配置
	dbConfigs := []DBConfig{
		{
			Name:     "user",
			Source:   bc.Data.UserDb.Source,
			Tables:   []string{"user", "user_address"},
			OutPath:  filepath.Join(baseDir, "user", "query"),
			ModelPkg: filepath.Join(baseDir, "user", "model"),
		},
		{
			Name:     "product",
			Source:   bc.Data.ProductDb.Source,
			Tables:   []string{"product", "category"},
			OutPath:  filepath.Join(baseDir, "product", "query"),
			ModelPkg: filepath.Join(baseDir, "product", "model"),
		},
		{
			Name:     "core",
			Source:   bc.Data.CoreDb.Source,
			Tables:   []string{"seckill_activity", "seckill_sku", "coupon", "seckill_order", "order_shipping"},
			OutPath:  filepath.Join(baseDir, "core", "query"),
			ModelPkg: filepath.Join(baseDir, "core", "model"),
		},
		{
			Name:     "pay",
			Source:   bc.Data.PayDb.Source,
			Tables:   []string{"pay_info"},
			OutPath:  filepath.Join(baseDir, "pay", "query"),
			ModelPkg: filepath.Join(baseDir, "pay", "model"),
		},
	}

	// 为每个数据库生成代码
	for _, dbc := range dbConfigs {
		fmt.Printf("Generating code for %s database...\n", dbc.Name)

		// 确保输出目录存在
		ensureDir(dbc.OutPath)
		ensureDir(dbc.ModelPkg)

		// 创建生成器配置
		g := gen.NewGenerator(gen.Config{
			OutPath:      dbc.OutPath,
			ModelPkgPath: dbc.ModelPkg,
			Mode:         gen.WithDefaultQuery | gen.WithQueryInterface,

			// 字段配置
			FieldNullable:     true, // 字段允许为空
			FieldCoverable:    true, // 字段可覆盖
			FieldSignable:     true, // 字段可签名
			FieldWithIndexTag: true, // 生成索引标签
			FieldWithTypeTag:  true, // 生成类型标签
		})

		// 连接数据库
		db := connectDB(dbc.Source)
		g.UseDB(db)

		// 生成指定表
		models := make([]interface{}, len(dbc.Tables))
		for i, table := range dbc.Tables {
			models[i] = g.GenerateModel(table)
		}

		g.ApplyBasic(models...)

		// 执行生成
		g.Execute()

		fmt.Printf("%s database code generation completed!\n", dbc.Name)

	}
	fmt.Println("\n All code generation completed successfully")
}

func ensureDir(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("create dir failed: %v", err)
		}
	}
}
