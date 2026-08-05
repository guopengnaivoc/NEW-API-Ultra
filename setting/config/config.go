package config

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

type configSnapshot struct {
	value interface{}
}

type configEntry struct {
	updateMutex sync.Mutex
	current     atomic.Pointer[configSnapshot]
}

type configValidator interface {
	Validate() error
}

type rejectedConfigFieldError struct {
	field string
}

func (e *rejectedConfigFieldError) Error() string {
	return fmt.Sprintf("config field %q must be a finite number", e.field)
}

// ConfigManager 统一管理所有配置
type ConfigManager struct {
	configs map[string]*configEntry
	mutex   sync.RWMutex
}

var GlobalConfig = NewConfigManager()

func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs: make(map[string]*configEntry),
	}
}

// Register 注册一个配置模块
func (cm *ConfigManager) Register(name string, config interface{}) {
	entry := &configEntry{}
	entry.current.Store(&configSnapshot{value: config})

	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.configs[name] = entry
}

func (cm *ConfigManager) lookup(name string) *configEntry {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.configs[name]
}

// Get 获取指定配置模块
func (cm *ConfigManager) Get(name string) interface{} {
	entry := cm.lookup(name)
	if entry == nil {
		return nil
	}
	snapshot := entry.current.Load()
	if snapshot == nil {
		return nil
	}
	return snapshot.value
}

// Snapshot returns the current typed snapshot for a globally registered
// module. A missing registration or type mismatch is a programming error.
func Snapshot[T any](name string) *T {
	value := GlobalConfig.Get(name)
	snapshot, ok := value.(*T)
	if !ok || snapshot == nil {
		panic(fmt.Sprintf("config snapshot %q has type %T, want %T", name, value, (*T)(nil)))
	}
	return snapshot
}

func cloneConfig(config interface{}) (interface{}, error) {
	value := reflect.ValueOf(config)
	if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() ||
		value.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("config must be a non-nil pointer to a struct, got %T", config)
	}

	cloned := reflect.New(value.Elem().Type()).Interface()
	data, err := common.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal config %T: %w", config, err)
	}
	if err := common.Unmarshal(data, cloned); err != nil {
		return nil, fmt.Errorf("unmarshal config %T: %w", config, err)
	}
	return cloned, nil
}

func applyConfigUpdate(entry *configEntry, values map[string]string, publish bool) error {
	entry.updateMutex.Lock()
	defer entry.updateMutex.Unlock()

	current := entry.current.Load()
	if current == nil {
		return fmt.Errorf("config entry has no current snapshot")
	}
	next, err := cloneConfig(current.value)
	if err != nil {
		return err
	}
	if err := updateConfigFromMap(next, values); err != nil {
		return err
	}
	if validator, ok := next.(configValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	if publish {
		entry.current.Store(&configSnapshot{value: next})
	}
	return nil
}

// Validate checks whether a registered module can accept an update without
// publishing it.
func (cm *ConfigManager) Validate(name string, values map[string]string) (bool, error) {
	entry := cm.lookup(name)
	if entry == nil {
		return false, nil
	}
	return true, applyConfigUpdate(entry, values, false)
}

// Update publishes an immutable snapshot for a registered configuration
// module. Unknown modules are reported as not updated so legacy options can
// continue through their existing dispatch path.
func (cm *ConfigManager) Update(name string, values map[string]string) (bool, error) {
	entry := cm.lookup(name)
	if entry == nil {
		return false, nil
	}
	return true, applyConfigUpdate(entry, values, true)
}

func (cm *ConfigManager) registeredEntries() map[string]*configEntry {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	entries := make(map[string]*configEntry, len(cm.configs))
	for name, entry := range cm.configs {
		entries[name] = entry
	}
	return entries
}

// LoadFromDB 从数据库加载配置
func (cm *ConfigManager) LoadFromDB(options map[string]string) error {
	for name, entry := range cm.registeredEntries() {
		prefix := name + "."
		configMap := make(map[string]string)

		// 收集属于此配置的所有选项
		for key, value := range options {
			if strings.HasPrefix(key, prefix) {
				configKey := strings.TrimPrefix(key, prefix)
				configMap[configKey] = value
			}
		}

		// 如果找到配置项，则更新配置
		if len(configMap) > 0 {
			for len(configMap) > 0 {
				if err := applyConfigUpdate(entry, configMap, true); err != nil {
					var fieldErr *rejectedConfigFieldError
					if errors.As(err, &fieldErr) {
						common.SysError("skipping invalid config " + name + "." + fieldErr.field + ": " + err.Error())
						delete(configMap, fieldErr.field)
						continue
					}
					common.SysError("failed to update config " + name + ": " + err.Error())
				}
				break
			}
		}
	}

	return nil
}

// SaveToDB 将配置保存到数据库。每个模块单独读取一致快照，但跨模块不提供事务快照。
func (cm *ConfigManager) SaveToDB(updateFunc func(key, value string) error) error {
	for name, entry := range cm.registeredEntries() {
		snapshot := entry.current.Load()
		if snapshot == nil {
			continue
		}
		configMap, err := configToMap(snapshot.value)
		if err != nil {
			return err
		}

		for key, value := range configMap {
			dbKey := name + "." + key
			if err := updateFunc(dbKey, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// 辅助函数：将配置对象转换为map
func configToMap(config interface{}) (map[string]string, error) {
	result := make(map[string]string)

	val := reflect.ValueOf(config)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil, nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		// 处理不同类型的字段
		var strValue string
		switch field.Kind() {
		case reflect.String:
			strValue = field.String()
		case reflect.Bool:
			strValue = strconv.FormatBool(field.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			strValue = strconv.FormatInt(field.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			strValue = strconv.FormatUint(field.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			strValue = strconv.FormatFloat(field.Float(), 'f', -1, field.Type().Bits())
		case reflect.Ptr:
			// 处理指针类型：如果非 nil，序列化指向的值
			if !field.IsNil() {
				bytes, err := common.Marshal(field.Interface())
				if err != nil {
					return nil, err
				}
				strValue = string(bytes)
			} else {
				// nil 指针序列化为 "null"
				strValue = "null"
			}
		case reflect.Map, reflect.Slice, reflect.Struct:
			// 复杂类型使用JSON序列化
			bytes, err := common.Marshal(field.Interface())
			if err != nil {
				return nil, err
			}
			strValue = string(bytes)
		default:
			// 跳过不支持的类型
			continue
		}

		result[key] = strValue
	}

	return result, nil
}

// 辅助函数：从map更新配置对象
func updateConfigFromMap(config interface{}, configMap map[string]string) error {
	val := reflect.ValueOf(config)
	if val.Kind() != reflect.Ptr {
		return nil
	}
	val = val.Elem()

	if val.Kind() != reflect.Struct {
		return nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		// 检查map中是否有对应的值
		strValue, ok := configMap[key]
		if !ok {
			continue
		}

		// 根据字段类型设置值
		if !field.CanSet() {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			field.SetString(strValue)
		case reflect.Bool:
			boolValue, err := strconv.ParseBool(strValue)
			if err != nil {
				continue
			}
			field.SetBool(boolValue)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			intValue, err := strconv.ParseInt(strValue, 10, 64)
			if err != nil {
				// 兼容 float 格式的字符串（如 "2.000000"）
				floatValue, fErr := strconv.ParseFloat(strValue, 64)
				if fErr != nil {
					continue
				}
				intValue = int64(floatValue)
			}
			field.SetInt(intValue)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			uintValue, err := strconv.ParseUint(strValue, 10, 64)
			if err != nil {
				// 兼容 float 格式的字符串
				floatValue, fErr := strconv.ParseFloat(strValue, 64)
				if fErr != nil || floatValue < 0 {
					continue
				}
				uintValue = uint64(floatValue)
			}
			field.SetUint(uintValue)
		case reflect.Float32, reflect.Float64:
			floatValue, err := strconv.ParseFloat(strValue, field.Type().Bits())
			if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
				return &rejectedConfigFieldError{field: key}
			}
			if err != nil {
				continue
			}
			field.SetFloat(floatValue)
		case reflect.Ptr:
			// 处理指针类型
			if strValue == "null" {
				field.Set(reflect.Zero(field.Type()))
			} else {
				// 如果指针是 nil，需要先初始化
				if field.IsNil() {
					field.Set(reflect.New(field.Type().Elem()))
				}
				// 反序列化到指针指向的值
				err := common.Unmarshal([]byte(strValue), field.Interface())
				if err != nil {
					continue
				}
			}
		case reflect.Map:
			// JSON decoding merges into existing maps (keeps old keys that are
			// absent from the new JSON). Allocate a fresh map so removed keys
			// are properly cleared.
			fresh := reflect.New(field.Type())
			if err := common.Unmarshal([]byte(strValue), fresh.Interface()); err != nil {
				continue
			}
			field.Set(fresh.Elem())
		case reflect.Slice, reflect.Struct:
			err := common.Unmarshal([]byte(strValue), field.Addr().Interface())
			if err != nil {
				continue
			}
		}
	}

	return nil
}

// ConfigToMap 将配置对象转换为map（导出函数）
func ConfigToMap(config interface{}) (map[string]string, error) {
	return configToMap(config)
}

// UpdateConfigFromMap 从map更新调用方拥有的独立配置对象；不得用于修改已发布的管理器快照。
func UpdateConfigFromMap(config interface{}, configMap map[string]string) error {
	return updateConfigFromMap(config, configMap)
}

// ExportAllConfigs 导出所有已注册的配置为扁平结构。每个模块单独读取一致快照，
// 但返回结果不是跨模块事务快照。
func (cm *ConfigManager) ExportAllConfigs() map[string]string {
	result := make(map[string]string)

	for name, entry := range cm.registeredEntries() {
		snapshot := entry.current.Load()
		if snapshot == nil {
			continue
		}
		configMap, err := ConfigToMap(snapshot.value)
		if err != nil {
			continue
		}

		// 使用 "模块名.配置项" 的格式添加到结果中
		for key, value := range configMap {
			result[name+"."+key] = value
		}
	}

	return result
}
