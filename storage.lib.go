package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"slices"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var (
	sql_type_conversion = map[string]string{
		"string":   "TEXT",
		"[]string": "TEXT",
		"int":      "INTEGER",
		"uint64":   "INTEGER",
		"[]uint8":  "BLOB", // Bonus Type for Screenshot object
	}
)

type Matrix [][]interface{}

type Vault struct {
	db       *sql.DB
	location string
}

type Table struct {
	// name the table's name.
	name    string
	headers map[string]string // column name -> SQL type mapping
	rows    Matrix
}

/*
headers = {
	"column_name": "TEXT",
	"column_name": "TEXT",
	"column_name": "INTEGER",
}
*/

// Convert (most) structs and struct slices to a table, so they can be stored in the database
/* Alright, quick note from rondotdll
 * This code is admittedly a bit of a mess, but just know it works.
 * In order to keep this function from becoming a green soup, I'm going to
 * explain how it works here.

 * 1.) The first thing we do is create a new Table type, which is just a generic
 *     struct that holds the table's name, headers, and rows.
 * 2.) We then use reflection to get the type and value(s) of the input object.
 * 3a.) If the input object is a slice, we first iterate through the outer slice
 *  	  (each row) and then iterate through said row's columns.
 * 3b.) If the input object is a struct, we iterate through the struct's property names.
 * 3c.) If the input object is neither a slice nor a struct, we return an empty table.
 * 4.) We then append the current column to the current row.
 *
 * Note this code will NOT work for any structs that contain slices, as the current code
 * does not support nested slices.

 */

// ToTable converts anything into a Table.
func ToTable(this interface{}) Table {
	// 1
	result := Table{
		headers: make(map[string]string),
		rows:    make(Matrix, 0),
	}

	// 2 - set the table's name to the name of the struct type.
	datatype, value := reflect.TypeOf(this), reflect.ValueOf(this)
	result.name = datatype.Name()

	// 3a
	// if our input is a builtin data type (a slice)...
	if result.name == "" && datatype.Kind() == reflect.Slice {
		for row_id := 0; row_id < value.Len(); row_id++ {
			row_result := make([]interface{}, 0)

			// iterate through the value's fields...
			for field_index := 0; field_index < value.Index(row_id).Type().NumField(); field_index++ {
				if row_id == 0 {
					// map the builtin type to an SQL equivalent.
					sqlType := GetSQLDataType(value.Index(0).Type().Field(field_index))

					// add the field name and the respective mapped type to result.headers.
					result.headers[value.Index(0).Type().Field(field_index).Name] = sqlType
				}

				// used for debugging
				//println(value.Index(row_id).Field(field_index).Interface())

				row_result = append(row_result, value.Index(row_id).Field(field_index).Interface())
			}
			result.rows = append(result.rows, row_result)
		}
		// 3b
	} else {
		// if the input is not a builtin data type...
		result.rows = make(Matrix, 0)

		// iterate through the struct's property names
		for field_index := 0; field_index < value.Type().NumField(); field_index++ {
			// { FieldName: FieldType }
			result.headers[value.Type().Field(field_index).Name] = GetSQLDataType(value.Type().Field(field_index))
			result.rows[0] = append(result.rows[0], value.Interface())
		}

	}

	return result
}

// converts `val` value to string
func sformat(val interface{}) string {
	value := reflect.ValueOf(val)
	switch value.Kind() {
	case reflect.String:
		return fmt.Sprintf("%s ", value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d ", value.Int())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%f ", value.Float())
	case reflect.Bool:
		return fmt.Sprintf("%t ", value.Bool())
	default:
		return fmt.Sprintf("%v ", value.Interface())
	}
}

func (this *Vault) Init(dbpath string) *Vault {
	this.db, _ = sql.Open("sqlite3", dbpath)
	this.location = dbpath
	return this
}

// get's the corresponding SQL data type for a given Go struct field
func GetSQLDataType(input reflect.StructField) string {
	return sql_type_conversion[input.Type.Name()]
}

// Pushes and stores a Table in the database
func (this *Vault) StoreTable(table Table) *Vault {
	columns := "(id INTEGER PRIMARY KEY"
	for hname, htype := range table.headers {
		columns += ", " + hname + " " + htype
	}
	this.db.Exec("CREATE TABLE IF NOT EXISTS" + table.name + columns + ")")

	for _, row := range table.rows {
		var values []string
		for _, col := range row {
			values = append(values, sformat(col))
		}

		this.SQL("INSERT INTO " + table.name + " VALUES (" + strings.Join(values, ",") + ")")
	}
	return this
}

// SQL runs a custom SQL Query
func (this *Vault) SQL(query string) interface{} {
	rows, _ := this.db.Query(query)
	return rows
}

// Destroy Removes the databse from the system
func (this *Vault) Destroy() *Vault {
	this.db.Close()
	os.Remove(this.location)
	return this
}

// Signs (RSA Encrypts) & closes the database
func (this *Vault) Sign() *Vault {
	this.db.Close()

	// read the database
	raw, _ := os.ReadFile(this.location)

	// placeholder for RSA Big Int (assigned by builder)
	var rsa_n = big.NewInt(__BIGINT_x64)

	// placeholder for public key
	key := rsa.PublicKey{
		N: rsa_n,   // Modulus
		E: 0x10001, // Exponent [Default]
	}

	// Encrypt the raw using the public key
	cipherSlice, _ := chunkAndEncryptOAEP(
		raw,
		&key,
	)

	slices.Reverse(cipherSlice)

	ciphertext := bytes.Join(cipherSlice, []byte(""))

	// Write the encrypted ciphertext to the output file
	os.WriteFile(this.location, ciphertext, 0644)

	return this
}

// paste it here
func chunkAndEncryptOAEP(message []byte, pub *rsa.PublicKey) ([][]byte, error) {
	chunkSize := pub.Size() - 2*sha256.Size
	chunks := make([][]byte, 0, (len(message)+chunkSize-1)/chunkSize)

	for len(message) > chunkSize {
		chunk := message[:chunkSize]  // the first `chunkSize` bytes
		message = message[chunkSize:] // everything after `chunkSize` bytes

		encryptedChunk, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, chunk, nil)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, encryptedChunk)
	}

	// Encrypt the final chunk
	if len(message) > 0 {
		encryptedChunk, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, message, nil)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, encryptedChunk)
	}

	return chunks, nil
}
