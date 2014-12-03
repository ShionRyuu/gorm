package gorm

import (
	"errors"
	"fmt"
	"go/ast"
	"strings"
	"time"

	"reflect"
	"regexp"
)

// SQL 作用域，存放一个特定场景（可能有多个SQL操作）的数据
type Scope struct {
	Value           interface{}       //
	indirectValue   *reflect.Value    // Scope指向的实际的值
	Search          *search           // 查找信息
	Sql             string            //
	SqlVars         []interface{}     //
	db              *DB               //
	skipLeft        bool              //
	primaryKeyField *Field            // 主键Field
	instanceId      string            //
	fields          map[string]*Field //
}

// 获取并缓存Scope指向的实际的值
func (scope *Scope) IndirectValue() reflect.Value {
	if scope.indirectValue == nil {
		value := reflect.Indirect(reflect.ValueOf(scope.Value))
		scope.indirectValue = &value
	}
	return *scope.indirectValue
}

// NewScope create scope for callbacks, including DB's search information
func (db *DB) NewScope(value interface{}) *Scope {
	db.Value = value
	return &Scope{db: db, Search: db.search, Value: value}
}

func (scope *Scope) NeedPtr() *Scope {
	reflectKind := reflect.ValueOf(scope.Value).Kind()
	if !((reflectKind == reflect.Invalid) || (reflectKind == reflect.Ptr)) {
		err := errors.New(fmt.Sprintf("%v %v\n", fileWithLineNum(), "using unaddressable value"))
		scope.Err(err)
		fmt.Printf(err.Error())
	}
	return scope
}

// New create a new Scope without search information
func (scope *Scope) New(value interface{}) *Scope {
	return &Scope{db: scope.db, Search: &search{}, Value: value}
}

// NewDB create a new DB without search information
func (scope *Scope) NewDB() *DB {
	return scope.db.new()
}

// DB get *sql.DB
func (scope *Scope) DB() sqlCommon {
	return scope.db.db
}

// SkipLeft skip remaining callbacks
func (scope *Scope) SkipLeft() {
	scope.skipLeft = true
}

// Quote used to quote database column name according to database dialect
// 根据Dialect（方言） 转义数据库列名
func (scope *Scope) Quote(str string) string {
	return scope.Dialect().Quote(str)
}

// Dialect get dialect
// 获取Dialect（方言）
func (scope *Scope) Dialect() Dialect {
	return scope.db.parent.dialect
}

// 设置数据库执行错误
func (scope *Scope) Err(err error) error {
	if err != nil {
		scope.db.err(err)
	}
	return err
}

// 打印日志信息
func (scope *Scope) Log(v ...interface{}) {
	scope.db.log(v...)
}

// 检查是否数据库执行出错
func (scope *Scope) HasError() bool {
	return scope.db.Error != nil
}

// 获取主键对应的*Field
func (scope *Scope) PrimaryKeyField() *Field {
	if scope.primaryKeyField == nil {
		var indirectValue = scope.IndirectValue()

		// Slice需要取到实际元素。。。
		// IndirectValue().Type().Elem() ->
		//
		clone := scope
		if indirectValue.Kind() == reflect.Slice {
			clone = scope.New(reflect.New(indirectValue.Type().Elem()).Elem().Interface())
		}

		for _, field := range clone.Fields() {
			if field.IsPrimaryKey {
				scope.primaryKeyField = field
				break
			}
		}
	}

	return scope.primaryKeyField
}

// 获取主键的列名
func (scope *Scope) PrimaryKey() string {
	if field := scope.PrimaryKeyField(); field != nil {
		return field.DBName
	} else {
		return ""
	}
}

// 判断主键是否为空
func (scope *Scope) PrimaryKeyZero() bool {
	// reflect.ValueOf
	return isBlank(reflect.ValueOf(scope.PrimaryKeyValue()))
}

// 获取主键的值
func (scope *Scope) PrimaryKeyValue() interface{} {
	if field := scope.PrimaryKeyField(); field != nil {
		return field.Field.Interface() // 将主键的值以interface{}返回
	} else {
		return 0
	}
}

// HasColumn to check if has column
func (scope *Scope) HasColumn(column string) (hasColumn bool) {
	clone := scope
	if scope.IndirectValue().Kind() == reflect.Slice {
		value := reflect.New(scope.IndirectValue().Type().Elem()).Interface()
		clone = scope.New(value)
	}

	//
	dbName := ToSnake(column)

	// 判断是否存在dbName的字段
	_, hasColumn = clone.Fields(false)[dbName]

	return
}

// FieldValueByName to get column's value and existence
func (scope *Scope) FieldValueByName(name string) (interface{}, error) {
	return FieldValueByName(name, scope.Value)
}

// SetColumn to set the column's value
func (scope *Scope) SetColumn(column interface{}, value interface{}) error {
	if field, ok := column.(*Field); ok {
		return field.Set(value)
	} else if str, ok := column.(string); ok {
		if scope.Value == nil {
			return errors.New("scope value must not be nil for string columns")
		}

		dbName := ToSnake(str)

		if field, ok := scope.Fields()[dbName]; ok {
			return field.Set(value)
		}
	}
	return errors.New("could not convert column to field")
}

// CallMethod invoke method with necessary argument
// 调用指定名字的方法
func (scope *Scope) CallMethod(name string) {
	if scope.Value == nil {
		return
	}

	call := func(value interface{}) {
		if fm := reflect.ValueOf(value).MethodByName(name); fm.IsValid() {
			switch f := fm.Interface().(type) {
			case func():
				f()
			case func(s *Scope):
				f(scope)
			case func(s *DB):
				f(scope.db.new())
			case func() error:
				scope.Err(f())
			case func(s *Scope) error:
				scope.Err(f(scope))
			case func(s *DB) error:
				scope.Err(f(scope.db.new()))
			default:
				scope.Err(errors.New(fmt.Sprintf("unsupported function %v", name)))
			}
		}
	}

	if values := scope.IndirectValue(); values.Kind() == reflect.Slice {
		for i := 0; i < values.Len(); i++ {
			// Index(i) 获取Slice等的第i个元素
			// Addr().Interface()
			call(values.Index(i).Addr().Interface())
		}
	} else {
		call(scope.Value)
	}
}

// AddToVars add value as sql's vars, gorm will escape them
func (scope *Scope) AddToVars(value interface{}) string {
	scope.SqlVars = append(scope.SqlVars, value)
	return scope.Dialect().BinVar(len(scope.SqlVars))
}

// TableName get table name
var pluralMapKeys = []*regexp.Regexp{regexp.MustCompile("ch$"), regexp.MustCompile("ss$"), regexp.MustCompile("sh$"), regexp.MustCompile("day$"), regexp.MustCompile("y$"), regexp.MustCompile("x$"), regexp.MustCompile("([^s])s?$")}
var pluralMapValues = []string{"ches", "sses", "shes", "days", "ies", "xes", "${1}s"}

func (scope *Scope) TableName() string {
	if scope.Search != nil && len(scope.Search.TableName) > 0 {
		return scope.Search.TableName
	} else {
		if scope.Value == nil {
			scope.Err(errors.New("can't get table name"))
			return ""
		}

		data := scope.IndirectValue()
		if data.Kind() == reflect.Slice {
			elem := data.Type().Elem()
			if elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			data = reflect.New(elem).Elem()
		}

		if fm := data.MethodByName("TableName"); fm.IsValid() {
			if v := fm.Call([]reflect.Value{}); len(v) > 0 {
				if result, ok := v[0].Interface().(string); ok {
					return result
				}
			}
		}

		str := ToSnake(data.Type().Name())

		if scope.db == nil || !scope.db.parent.singularTable {
			for index, reg := range pluralMapKeys {
				if reg.MatchString(str) {
					return reg.ReplaceAllString(str, pluralMapValues[index])
				}
			}
		}

		return str
	}
}

// 获取引用(``)表名
func (scope *Scope) QuotedTableName() string {
	if scope.Search != nil && len(scope.Search.TableName) > 0 {
		return scope.Search.TableName
	} else {
		keys := strings.Split(scope.TableName(), ".")
		for i, v := range keys {
			keys[i] = scope.Quote(v)
		}
		return strings.Join(keys, ".")
	}
}

// 构造组合条件查找的SQL语句
func (scope *Scope) CombinedConditionSql() string {
	return scope.joinsSql() + scope.whereSql() + scope.groupSql() +
		scope.havingSql() + scope.orderSql() + scope.limitSql() + scope.offsetSql()
}

// 获取指定名字的Field
func (scope *Scope) FieldByName(name string) (field *Field, ok bool) {
	for _, field := range scope.Fields() {
		if field.Name == name {
			return field, true
		}
	}
	return nil, false
}

func (scope *Scope) fieldFromStruct(fieldStruct reflect.StructField, withRelation bool) []*Field {
	var field Field
	field.Name = fieldStruct.Name

	// 字段的真实值
	// FieldByName
	value := scope.IndirectValue().FieldByName(fieldStruct.Name)
	// reflect.Indirect
	indirectValue := reflect.Indirect(value)
	field.Field = value
	field.IsBlank = isBlank(value)

	// Search for primary key tag identifier
	settings := parseTagSetting(fieldStruct.Tag.Get("gorm"))
	if _, ok := settings["PRIMARY_KEY"]; ok {
		field.IsPrimaryKey = true
	}

	// sql默认值
	if def, ok := parseTagSetting(fieldStruct.Tag.Get("sql"))["DEFAULT"]; ok {
		field.DefaultValue = def
	}

	// 保存标签
	field.Tag = fieldStruct.Tag

	// 是否有特殊指定列名
	if value, ok := settings["COLUMN"]; ok {
		field.DBName = value
	} else {
		field.DBName = ToSnake(fieldStruct.Name)
	}

	// 数据库系统特定标签
	tagIdentifier := "sql"
	if scope.db != nil {
		tagIdentifier = scope.db.parent.tagIdentifier
	}

	// 判断字段是否忽略
	if fieldStruct.Tag.Get(tagIdentifier) == "-" {
		field.IsIgnored = true
	}

	if !field.IsIgnored {
		// parse association
		if !indirectValue.IsValid() {
			indirectValue = reflect.New(value.Type())
		}
		typ := indirectValue.Type()
		scopeTyp := scope.IndirectValue().Type()

		foreignKey := SnakeToUpperCamel(settings["FOREIGNKEY"])
		foreignType := SnakeToUpperCamel(settings["FOREIGNTYPE"])
		associationForeignKey := SnakeToUpperCamel(settings["ASSOCIATIONFOREIGNKEY"])
		many2many := settings["MANY2MANY"]
		polymorphic := SnakeToUpperCamel(settings["POLYMORPHIC"])

		if polymorphic != "" {
			foreignKey = polymorphic + "Id"
			foreignType = polymorphic + "Type"
		}

		switch indirectValue.Kind() {
		case reflect.Slice: // 字段是Slice
			typ = typ.Elem()

			if (typ.Kind() == reflect.Struct) && withRelation { // 结构体Slice 且 保持关联
				if foreignKey == "" { // 默认外键 结构体名+"Id"
					foreignKey = scopeTyp.Name() + "Id"
				}
				if associationForeignKey == "" { // 默认关联外键 元素类型名+"Id"
					associationForeignKey = typ.Name() + "Id"
				}

				// if not many to many, foreign key could be null
				// 非多对多关系，外键不能为空，默认""
				if many2many == "" {
					if !reflect.New(typ).Elem().FieldByName(foreignKey).IsValid() {
						foreignKey = ""
					}
				}

				field.Relationship = &relationship{
					JoinTable:             many2many,
					ForeignKey:            foreignKey,
					ForeignType:           foreignType,
					AssociationForeignKey: associationForeignKey,
					Kind: "has_many",
				}

				if many2many != "" {
					field.Relationship.Kind = "many_to_many"
				}
			} else {
				field.IsNormal = true
			}
		case reflect.Struct: // 字段是结构体（Struct）
			if field.IsTime() || field.IsScanner() {
				field.IsNormal = true
			} else if _, ok := settings["EMBEDDED"]; ok || fieldStruct.Anonymous { // 匿名字段 或 EMBEDDED（嵌套）字段
				var fields []*Field
				if field.Field.CanAddr() { // 可寻址？？  递归获取
					for _, field := range scope.New(field.Field.Addr().Interface()).Fields() {
						field.DBName = field.DBName
						fields = append(fields, field)
					}
				}
				return fields
			} else if withRelation {
				var belongsToForeignKey, hasOneForeignKey, kind string

				if foreignKey == "" { // 没有指定外键
					belongsToForeignKey = field.Name + "Id"   // belongs to关系外键: 元素名+"Id"，外键位于本结构体
					hasOneForeignKey = scopeTyp.Name() + "Id" // has one关系外键: 结构体名+"Id"，外键位于字段类型的结构体
				} else {
					belongsToForeignKey = foreignKey
					hasOneForeignKey = foreignKey
				}

				// belongs to, has one关系区别：外键存放的位置，设置的顺序不同
				if scope.HasColumn(belongsToForeignKey) {
					foreignKey = belongsToForeignKey
					kind = "belongs_to"
				} else {
					foreignKey = hasOneForeignKey
					kind = "has_one"
				}

				field.Relationship = &relationship{ForeignKey: foreignKey, ForeignType: foreignType, Kind: kind}
			}
		default:
			field.IsNormal = true
		}
	}
	return []*Field{&field}
}

// Fields get value's fields
func (scope *Scope) Fields(noRelations ...bool) map[string]*Field {
	var withRelation = len(noRelations) == 0

	if withRelation && scope.fields != nil {
		return scope.fields
	}

	var fields = map[string]*Field{}
	//
	if scope.IndirectValue().IsValid() && scope.IndirectValue().Kind() == reflect.Struct {
		scopeTyp := scope.IndirectValue().Type()
		var hasPrimaryKey = false
		for i := 0; i < scopeTyp.NumField(); i++ {
			fieldStruct := scopeTyp.Field(i)       // 取第i个字段
			if !ast.IsExported(fieldStruct.Name) { // 字段未导出
				continue
			}
			for _, field := range scope.fieldFromStruct(fieldStruct, withRelation) {
				if field.IsPrimaryKey {
					hasPrimaryKey = true
				}
				if value, ok := fields[field.DBName]; ok { // 判断是否已经设置，防止相同的列名
					if value.IsIgnored {
						fields[field.DBName] = field
					} else if !value.IsIgnored {
						panic(fmt.Sprintf("Duplicated column name for %v (%v)\n", scope.typeName(), fileWithLineNum()))
					}
				} else {
					fields[field.DBName] = field
				}
			}
		}

		if !hasPrimaryKey {
			if field, ok := fields["id"]; ok {
				field.IsPrimaryKey = true
			}
		}
	}

	if withRelation {
		scope.fields = fields
	}

	return fields
}

// Raw set sql
func (scope *Scope) Raw(sql string) *Scope {
	scope.Sql = strings.Replace(sql, "$$", "?", -1)
	return scope
}

// Exec invoke sql
func (scope *Scope) Exec() *Scope {
	defer scope.Trace(NowFunc()) // 日志

	if !scope.HasError() {
		result, err := scope.DB().Exec(scope.Sql, scope.SqlVars...)
		if scope.Err(err) == nil { // 如果没有错误，获取并设置影响的行数
			if count, err := result.RowsAffected(); err == nil {
				scope.db.RowsAffected = count
			}
		}
	}
	return scope
}

// 设置变量
func (scope *Scope) Set(name string, value interface{}) *Scope {
	scope.db.InstantSet(name, value)
	return scope
}

// 获取变量
func (scope *Scope) Get(name string) (interface{}, bool) {
	return scope.db.Get(name)
}

// InstanceId get InstanceId for scope
func (scope *Scope) InstanceId() string {
	if scope.instanceId == "" {
		scope.instanceId = fmt.Sprintf("%v", &scope)
	}
	return scope.instanceId
}

// 设置跟InstanceId相关的变量
func (scope *Scope) InstanceSet(name string, value interface{}) *Scope {
	return scope.Set(name+scope.InstanceId(), value)
}

// 获取跟InstanceId相关的变量
func (scope *Scope) InstanceGet(name string) (interface{}, bool) {
	return scope.Get(name + scope.InstanceId())
}

// Trace print sql log
func (scope *Scope) Trace(t time.Time) {
	if len(scope.Sql) > 0 {
		scope.db.slog(scope.Sql, t, scope.SqlVars...)
	}
}

// 开始事务    一个完整的支持事务需要实现sqlCommon, sqlTx, sqlDb三个接口
func (scope *Scope) Begin() *Scope {
	if db, ok := scope.DB().(sqlDb); ok {
		if tx, err := db.Begin(); err == nil {
			//
			scope.db.db = interface{}(tx).(sqlCommon)
			// 事务标记
			scope.InstanceSet("gorm:started_transaction", true)
		}
	}
	return scope
}

// 出错回滚否则提交事务
func (scope *Scope) CommitOrRollback() *Scope {
	if _, ok := scope.InstanceGet("gorm:started_transaction"); ok {
		if db, ok := scope.db.db.(sqlTx); ok { // 实现了sqlTx接口
			if scope.HasError() {
				db.Rollback()
			} else {
				db.Commit()
			}
			scope.db.db = scope.db.parent.db
		}
	}
	return scope
}
