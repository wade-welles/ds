package ds

import (
	"encoding/gob"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/ecnepsnai/logtic"
	"github.com/etcd-io/bbolt"
)

// Register will register an instance of a struct with ds, creating a table (or opening an existing table) for this type
// at the specified file path.
func Register(o interface{}, filePath string, options *Options) (*Table, error) {
	typeOf := reflect.TypeOf(o)
	// TypeOf returns and empty string '' if you pass a pointer
	if len(typeOf.Name()) <= 0 {
		return nil, fmt.Errorf("unknown object type provided. Did you pass a pointer?")
	}

	// Gob will panic if you register the same object twice.
	// I think this is stupid, so we will recover from this if
	// registering the type panics.
	registerGobType(o)
	registerGobType(Options{})
	registerGobType(Config{})

	table := Table{
		Name:   typeOf.Name(),
		typeOf: typeOf,
		log:    logtic.Connect("ds(" + typeOf.Name() + ")"),
	}
	if options != nil {
		table.options = *options
	}

	var primaryKey string
	var indexes []string
	var uniques []string

	numFields := typeOf.NumField()
	if numFields == 0 {
		return nil, fmt.Errorf("type '%s' has no fields", typeOf.Name())
	}
	i := 0
	for i < numFields {
		field := typeOf.Field(i)
		tag := field.Tag.Get("ds")
		if len(tag) == 0 {
			i++
			continue
		}
		table.log.Debug("Field: %s, tag: %s", field.Name, tag)

		if strings.Contains(tag, "primary") {
			if len(primaryKey) > 0 {
				table.log.Error("Cannot specify multiple primary keys")
				return nil, fmt.Errorf("cannot specify multiple primary keys")
			}

			table.log.Debug("Primary key field '%s'", field.Name)
			primaryKey = field.Name
		} else if strings.Contains(tag, "index") {
			table.log.Debug("Adding indexed field '%s'", field.Name)
			indexes = append(indexes, field.Name)
		} else if strings.Contains(tag, "unique") {
			table.log.Debug("Adding unique field '%s'", field.Name)
			uniques = append(uniques, field.Name)
		} else {
			table.log.Error("Unknown struct tag '%s' on field '%s'", tag, field.Name)
			return nil, fmt.Errorf("unknown struct tag '%s' on field '%s'", tag, field.Name)
		}

		i++
	}

	if len(primaryKey) <= 0 {
		table.log.Error("A primary key is required")
		return nil, fmt.Errorf("a primary key is required")
	}

	table.primaryKey = primaryKey
	table.indexes = indexes
	table.uniques = uniques

	data, err := bbolt.Open(filePath, os.ModePerm, nil)
	if err != nil {
		table.log.Error("Error opening bolt database: %s", err.Error())
		return nil, err
	}
	table.data = data

	force := false
	if options != nil {
		force = options.force
	}

	err = data.Update(func(tx *bbolt.Tx) error {
		if err := table.initalizeConfig(tx, force); err != nil {
			table.log.Error("Error initializing config: %s", err.Error())
			return err
		}

		_, err = tx.CreateBucketIfNotExists(dataKey)
		if err != nil {
			table.log.Error("Error creating bucket '%s: %s", "data", err.Error())
			return err
		}
		if !table.options.DisableSorting {
			_, err = tx.CreateBucketIfNotExists(insertOrderKey)
			if err != nil {
				table.log.Error("Error creating bucket '%s: %s", insertOrderKey, err.Error())
				return err
			}
		}
		for _, index := range indexes {
			_, err = tx.CreateBucketIfNotExists([]byte(indexPrefix + index))
			if err != nil {
				table.log.Error("Error creating bucket '%s: %s", indexPrefix+index, err.Error())
				return err
			}
		}
		for _, unique := range uniques {
			_, err = tx.CreateBucketIfNotExists([]byte(uniquePrefix + unique))
			if err != nil {
				table.log.Error("Error creating bucket '%s: %s", uniquePrefix+unique, err.Error())
				return err
			}
		}

		return nil
	})
	if err != nil {
		table.log.Error("Error preparing bolt database: %s", err.Error())
		data.Close()
		return nil, err
	}

	table.log.Info("Datastore '%s' opened at '%s'", table.Name, filePath)

	return &table, nil
}

func registerGobType(o interface{}) {
	defer panicRecovery()
	gob.Register(o)
}

func panicRecovery() {
	recover()
}
