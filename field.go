package gorm

import (
	"database/sql"
	"errors"
	"reflect"
	"time"
)

type relationship struct {
	JoinTable             string
	ForeignKey            string
	ForeignType           string
	AssociationForeignKey string
	Kind                  string
}

// Field代表类的一个字段
type Field struct {
	Name         string            // 属性名
	DBName       string            // 数据库中的字段名
	Field        reflect.Value     //
	Tag          reflect.StructTag // 标签
	Relationship *relationship     //
	IsNormal     bool              //
	IsBlank      bool              // 可否为null
	IsIgnored    bool              // 是否忽略
	IsPrimaryKey bool              // 是否主键
	DefaultValue interface{}       // 默认值
}

// 是否为sql.Scanner类型
func (field *Field) IsScanner() bool {
	// 构造一个新实例，跟普通New一样指向的值是0值（zero value）   ？Interface() 需要一个值
	_, isScanner := reflect.New(field.Field.Type()).Interface().(sql.Scanner)
	return isScanner
}

// 是否为time.Time类型
func (field *Field) IsTime() bool {
	_, isTime := field.Field.Interface().(time.Time)
	return isTime
}

// 设置值
func (field *Field) Set(value interface{}) (err error) {
	if !field.Field.IsValid() {
		return errors.New("field value not valid")
	}

	// 是否可以寻址：可以通过Addr()获取地址（slice元素、可寻址数组的元素、可寻址struct{}的字段、指针解引用）
	if !field.Field.CanAddr() {
		return errors.New("field value not addressable")
	}

	// 如果value是reflect.Value类型，将value的值当作interface{}返回
	if rvalue, ok := value.(reflect.Value); ok {
		value = rvalue.Interface()
	}

	// 为什么获取Addr().Interface() ？？？
	// field.Field.Type().Interface() ？？？
	if scanner, ok := field.Field.Addr().Interface().(sql.Scanner); ok {
		scanner.Scan(value)
	} else if reflect.TypeOf(value).ConvertibleTo(field.Field.Type()) { // 类型转换！
		field.Field.Set(reflect.ValueOf(value).Convert(field.Field.Type()))
	} else {
		return errors.New("could not convert argument")
	}

	// 字段是否为空
	field.IsBlank = isBlank(field.Field)

	return
}
