/*
   Copyright 2022 Erigon contributors

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package mdbx

import (
	"context"
	"testing"

	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/log/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func BaseCase(t *testing.T) (kv.RwDB, kv.RwTx, kv.RwCursorDupSort) {
	path := t.TempDir()
	logger := log.New()
	table := "Table"
	db := NewMDBX(logger).Path(path).WithTablessCfg(func(defaultBuckets kv.TableCfg) kv.TableCfg {
		return kv.TableCfg{
			table:       kv.TableCfgItem{Flags: kv.DupSort},
			kv.Sequence: kv.TableCfgItem{},
		}
	}).MustOpen()

	tx, err := db.BeginRw(context.Background())
	require.NoError(t, err)

	c, err := tx.RwCursorDupSort(table)
	require.NoError(t, err)

	// Insert some dupsorted records
	require.NoError(t, c.Put([]byte("key1"), []byte("value1.1")))
	require.NoError(t, c.Put([]byte("key3"), []byte("value3.1")))
	require.NoError(t, c.Put([]byte("key1"), []byte("value1.3")))
	require.NoError(t, c.Put([]byte("key3"), []byte("value3.3")))

	return db, tx, c
}

func iteration(t *testing.T, c kv.RwCursorDupSort, start []byte, val []byte) ([]string, []string) {
	var keys []string
	var values []string
	var err error
	i := 0
	for k, v, err := start, val, err; k != nil; k, v, err = c.Next() {
		require.Nil(t, err)
		keys = append(keys, string(k))
		values = append(values, string(v))
		i += 1
	}
	for ind := i; ind > 1; ind-- {
		c.Prev()
	}

	return keys, values
}

func TestSeekBothRange(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	v, err := c.SeekBothRange([]byte("key2"), []byte("value1.2"))
	require.NoError(t, err)
	// SeekBothRange does extact match of the key, but range match of the value, so we get nil here
	require.Nil(t, v)

	v, err = c.SeekBothRange([]byte("key3"), []byte("value3.2"))
	require.NoError(t, err)
	require.Equal(t, "value3.3", string(v))
}

func TestLastDup(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	err := tx.Commit()
	require.NoError(t, err)
	roTx, err := db.BeginRo(context.Background())
	require.NoError(t, err)
	defer roTx.Rollback()

	roC, err := roTx.CursorDupSort("Table")
	require.NoError(t, err)
	defer roC.Close()

	var keys, vals []string
	var k, v []byte
	for k, _, err = roC.First(); err == nil && k != nil; k, _, err = roC.NextNoDup() {
		v, err = roC.LastDup()
		require.NoError(t, err)
		keys = append(keys, string(k))
		vals = append(vals, string(v))
	}
	require.NoError(t, err)
	require.Equal(t, []string{"key1", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.3"}, vals)
}

func TestPutGet(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	require.Error(t, c.Put([]byte(""), []byte("value1.1")))

	var v []byte
	v, err := tx.GetOne("Table", []byte("key1"))
	require.Nil(t, err)
	require.Equal(t, v, []byte("value1.1"))

	v, err = tx.GetOne("RANDOM", []byte("key1"))
	require.Error(t, err) // Error from non-existent bucket returns error
	require.Nil(t, v)
}

func TestIncrementRead(t *testing.T) {
	db, tx, c := BaseCase(t)

	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	_, err := tx.IncrementSequence(table, uint64(12))
	require.Nil(t, err)
	chaV, err := tx.ReadSequence(table)
	require.Nil(t, err)
	require.Equal(t, chaV, uint64(12))
	_, err = tx.IncrementSequence(table, uint64(240))
	require.Nil(t, err)
	chaV, err = tx.ReadSequence(table)
	require.Nil(t, err)
	require.Equal(t, chaV, uint64(252))
}

func TestHasDelete(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	require.NoError(t, tx.Put(table, []byte("key2"), []byte("value2.1")))
	require.NoError(t, tx.Put(table, []byte("key4"), []byte("value4.1")))
	require.NoError(t, tx.Put(table, []byte("key5"), []byte("value5.1")))

	c, err := tx.RwCursorDupSort(table)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, c.DeleteExact([]byte("key1"), []byte("value1.1")))
	require.NoError(t, c.DeleteExact([]byte("key1"), []byte("value1.3")))
	require.NoError(t, c.DeleteExact([]byte("key1"), []byte("value1.1"))) //valid but already deleted
	require.NoError(t, c.DeleteExact([]byte("key2"), []byte("value1.1"))) //valid key but wrong value

	res, err := tx.Has(table, []byte("key1"))
	require.Nil(t, err)
	require.False(t, res)

	res, err = tx.Has(table, []byte("key2"))
	require.Nil(t, err)
	require.True(t, res)

	res, err = tx.Has(table, []byte("key3"))
	require.Nil(t, err)
	require.True(t, res) //There is another key3 left

	res, err = tx.Has(table, []byte("k"))
	require.Nil(t, err)
	require.False(t, res)
}

func TestForAmount(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	require.NoError(t, tx.Put(table, []byte("key2"), []byte("value2.1")))
	require.NoError(t, tx.Put(table, []byte("key4"), []byte("value4.1")))
	require.NoError(t, tx.Put(table, []byte("key5"), []byte("value5.1")))

	var keys []string

	err := tx.ForAmount(table, []byte("key3"), uint32(2), func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	require.Nil(t, err)
	require.Equal(t, []string{"key3", "key3"}, keys)

	var keys1 []string

	err1 := tx.ForAmount(table, []byte("key1"), 100, func(k, v []byte) error {
		keys1 = append(keys1, string(k))
		return nil
	})
	require.Nil(t, err1)
	require.Equal(t, []string{"key1", "key1", "key2", "key3", "key3", "key4", "key5"}, keys1)

	var keys2 []string

	err2 := tx.ForAmount(table, []byte("value"), 100, func(k, v []byte) error {
		keys2 = append(keys2, string(k))
		return nil
	})
	require.Nil(t, err2)
	require.Nil(t, keys2)

	var keys3 []string

	err3 := tx.ForAmount(table, []byte("key1"), 0, func(k, v []byte) error {
		keys3 = append(keys3, string(k))
		return nil
	})
	require.Nil(t, err3)
	require.Nil(t, keys3)
}

func TestForPrefix(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	var keys []string

	err := tx.ForPrefix(table, []byte("key"), func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	require.Nil(t, err)
	require.Equal(t, []string{"key1", "key1", "key3", "key3"}, keys)

	var keys1 []string

	err = tx.ForPrefix(table, []byte("key1"), func(k, v []byte) error {
		keys1 = append(keys1, string(k))
		return nil
	})
	require.Nil(t, err)
	require.Equal(t, []string{"key1", "key1"}, keys1)

	var keys2 []string

	err = tx.ForPrefix(table, []byte("e"), func(k, v []byte) error {
		keys2 = append(keys2, string(k))
		return nil
	})
	require.Nil(t, err)
	require.Nil(t, keys2)
}

func TestAppendFirstLast(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	require.Error(t, tx.Append(table, []byte("key2"), []byte("value2.1")))
	require.NoError(t, tx.Append(table, []byte("key6"), []byte("value6.1")))
	require.Error(t, tx.Append(table, []byte("key4"), []byte("value4.1")))
	require.NoError(t, tx.AppendDup(table, []byte("key2"), []byte("value1.11")))

	k, v, err := c.First()
	require.Nil(t, err)
	require.Equal(t, k, []byte("key1"))
	require.Equal(t, v, []byte("value1.1"))

	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1", "key2", "key3", "key3", "key6"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3", "value1.11", "value3.1", "value3.3", "value6.1"}, values)

	k, v, err = c.Last()
	require.Nil(t, err)
	require.Equal(t, k, []byte("key6"))
	require.Equal(t, v, []byte("value6.1"))

	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key6"}, keys)
	require.Equal(t, []string{"value6.1"}, values)
}

func TestNextPrevCurrent(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	k, v, err := c.First()
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.Next()
	require.Equal(t, []byte("key1"), k)
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.Current()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.1", "value3.3"}, values)
	require.Equal(t, k, []byte("key1"))
	require.Equal(t, v, []byte("value1.3"))

	k, v, err = c.Next()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key3", "key3"}, keys)
	require.Equal(t, []string{"value3.1", "value3.3"}, values)

	k, v, err = c.Prev()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.Current()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.Prev()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3", "value3.1", "value3.3"}, values)

	err = c.DeleteCurrent()
	require.Nil(t, err)
	k, v, err = c.Current()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.3", "value3.1", "value3.3"}, values)

}

func TestSeek(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	k, v, err := c.Seek([]byte("k"))
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.Seek([]byte("key3"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key3", "key3"}, keys)
	require.Equal(t, []string{"value3.1", "value3.3"}, values)

	k, v, err = c.Seek([]byte("xyz"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Nil(t, keys)
	require.Nil(t, values)
}

func TestSeekExact(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	k, v, err := c.SeekExact([]byte("key3"))
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key3", "key3"}, keys)
	require.Equal(t, []string{"value3.1", "value3.3"}, values)

	k, v, err = c.SeekExact([]byte("key"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Nil(t, keys)
	require.Nil(t, values)
}

func TestSeekBothExact(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	k, v, err := c.SeekBothExact([]byte("key1"), []byte("value1.2"))
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Nil(t, keys)
	require.Nil(t, values)

	k, v, err = c.SeekBothExact([]byte("key2"), []byte("value1.1"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Nil(t, keys)
	require.Nil(t, values)

	k, v, err = c.SeekBothExact([]byte("key1"), []byte("value1.1"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1", "key3", "key3"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3", "value3.1", "value3.3"}, values)

	k, v, err = c.SeekBothExact([]byte("key3"), []byte("value3.3"))
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key3"}, keys)
	require.Equal(t, []string{"value3.3"}, values)
}

func TestNextDups(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	table := "Table"

	c, err := tx.RwCursorDupSort(table)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, c.DeleteExact([]byte("key1"), []byte("value1.1")))
	require.NoError(t, c.DeleteExact([]byte("key1"), []byte("value1.3")))
	require.NoError(t, c.DeleteExact([]byte("key3"), []byte("value3.1"))) //valid but already deleted
	require.NoError(t, c.DeleteExact([]byte("key3"), []byte("value3.3"))) //valid key but wrong value

	require.NoError(t, tx.Put(table, []byte("key2"), []byte("value1.1")))
	require.NoError(t, c.Put([]byte("key2"), []byte("value1.2")))
	require.NoError(t, c.Put([]byte("key3"), []byte("value1.6")))
	require.NoError(t, c.Put([]byte("key"), []byte("value1.7")))

	k, v, err := c.Current()
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key", "key2", "key2", "key3"}, keys)
	require.Equal(t, []string{"value1.7", "value1.1", "value1.2", "value1.6"}, values)

	v, err = c.FirstDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key", "key2", "key2", "key3"}, keys)
	require.Equal(t, []string{"value1.7", "value1.1", "value1.2", "value1.6"}, values)

	k, v, err = c.NextNoDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key2", "key2", "key3"}, keys)
	require.Equal(t, []string{"value1.1", "value1.2", "value1.6"}, values)

	k, v, err = c.NextDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key2", "key3"}, keys)
	require.Equal(t, []string{"value1.2", "value1.6"}, values)

	v, err = c.LastDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key2", "key3"}, keys)
	require.Equal(t, []string{"value1.2", "value1.6"}, values)

	k, v, err = c.NextDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Nil(t, keys)
	require.Nil(t, values)

	k, v, err = c.NextNoDup()
	require.Nil(t, err)
	keys, values = iteration(t, c, k, v)
	require.Equal(t, []string{"key3"}, keys)
	require.Equal(t, []string{"value1.6"}, values)
}

func TestCurrentDup(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	count, err := c.CountDuplicates()
	require.Nil(t, err)
	require.Equal(t, count, uint64(2))

	require.Error(t, c.PutNoDupData([]byte("key3"), []byte("value3.3")))
	require.NoError(t, c.DeleteCurrentDuplicates())

	k, v, err := c.SeekExact([]byte("key1"))
	require.Nil(t, err)
	keys, values := iteration(t, c, k, v)
	require.Equal(t, []string{"key1", "key1"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3"}, values)

	require.Equal(t, []string{"key1", "key1"}, keys)
	require.Equal(t, []string{"value1.1", "value1.3"}, values)
}

func TestDupDelete(t *testing.T) {
	db, tx, c := BaseCase(t)
	defer db.Close()
	defer tx.Rollback()
	defer c.Close()

	k, _, err := c.Current()
	require.Nil(t, err)
	require.Equal(t, []byte("key3"), k)

	err = c.DeleteCurrentDuplicates()
	require.Nil(t, err)

	err = c.Delete([]byte("key1"))
	require.Nil(t, err)

	count, err := c.Count()
	require.Nil(t, err)
	assert.Zero(t, count)
}
