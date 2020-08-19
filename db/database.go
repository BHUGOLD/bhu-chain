package bhudb

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bhu/bhu-chain/logger"
	"github.com/bhu/bhu-chain/logger/glog"
	"github.com/bhu/bhu-chain/metrics"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"

	gometrics "github.com/rcrowley/go-metrics"
)

var OpenFileLimit = 64

type LDBDatabase struct {
	fn string
	db *leveldb.DB

	getTimer       gometrics.Timer
	putTimer       gometrics.Timer
	delTimer       gometrics.Timer
	missMeter      gometrics.Meter
	readMeter      gometrics.Meter
	writeMeter     gometrics.Meter
	compTimeMeter  gometrics.Meter
	compReadMeter  gometrics.Meter
	compWriteMeter gometrics.Meter

	quitLock sync.Mutex
	quitChan chan chan error
}

func NewLDBDatabase(file string) (*LDBDatabase, error) {

	db, err := leveldb.OpenFile(file, &opt.Options{OpenFilesCacheCapacity: OpenFileLimit})

	if _, iscorrupted := err.(*errors.ErrCorrupted); iscorrupted {
		db, err = leveldb.RecoverFile(file, nil)
	}

	if err != nil {
		return nil, err
	}
	return &LDBDatabase{
		fn: file,
		db: db,
	}, nil
}

func (self *LDBDatabase) Put(key []byte, value []byte) error {

	if self.putTimer != nil {
		defer self.putTimer.UpdateSince(time.Now())
	}

	if self.writeMeter != nil {
		self.writeMeter.Mark(int64(len(value)))
	}
	return self.db.Put(key, value, nil)
}

func (self *LDBDatabase) Get(key []byte) ([]byte, error) {

	if self.getTimer != nil {
		defer self.getTimer.UpdateSince(time.Now())
	}

	dat, err := self.db.Get(key, nil)
	if err != nil {
		if self.missMeter != nil {
			self.missMeter.Mark(1)
		}
		return nil, err
	}

	if self.readMeter != nil {
		self.readMeter.Mark(int64(len(dat)))
	}
	return dat, nil

}

func (self *LDBDatabase) Delete(key []byte) error {

	if self.delTimer != nil {
		defer self.delTimer.UpdateSince(time.Now())
	}

	return self.db.Delete(key, nil)
}

func (self *LDBDatabase) NewIterator() iterator.Iterator {
	return self.db.NewIterator(nil, nil)
}

func (self *LDBDatabase) Flush() error {
	return nil
}

func (self *LDBDatabase) Close() {

	self.quitLock.Lock()
	defer self.quitLock.Unlock()

	if self.quitChan != nil {
		errc := make(chan error)
		self.quitChan <- errc
		if err := <-errc; err != nil {
			glog.V(logger.Error).Infof("metrics failure in '%s': %v\n", self.fn, err)
		}
	}

	if err := self.Flush(); err != nil {
		glog.V(logger.Error).Infof("flushing '%s' failed: %v\n", self.fn, err)
	}
	self.db.Close()
	glog.V(logger.Error).Infoln("flushed and closed db:", self.fn)
}

func (self *LDBDatabase) LDB() *leveldb.DB {
	return self.db
}

func (self *LDBDatabase) Meter(prefix string) {

	self.getTimer = metrics.NewTimer(prefix + "user/gets")
	self.putTimer = metrics.NewTimer(prefix + "user/puts")
	self.delTimer = metrics.NewTimer(prefix + "user/dels")
	self.missMeter = metrics.NewMeter(prefix + "user/misses")
	self.readMeter = metrics.NewMeter(prefix + "user/reads")
	self.writeMeter = metrics.NewMeter(prefix + "user/writes")
	self.compTimeMeter = metrics.NewMeter(prefix + "compact/time")
	self.compReadMeter = metrics.NewMeter(prefix + "compact/input")
	self.compWriteMeter = metrics.NewMeter(prefix + "compact/output")

	self.quitLock.Lock()
	self.quitChan = make(chan chan error)
	self.quitLock.Unlock()

	go self.meter(3 * time.Second)
}

func (self *LDBDatabase) meter(refresh time.Duration) {

	counters := make([][]float64, 2)
	for i := 0; i < 2; i++ {
		counters[i] = make([]float64, 3)
	}

	for i := 1; ; i++ {

		stats, err := self.db.GetProperty("leveldb.stats")
		if err != nil {
			glog.V(logger.Error).Infof("failed to read database stats: %v", err)
			return
		}

		lines := strings.Split(stats, "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[0]) != "Compactions" {
			lines = lines[1:]
		}
		if len(lines) <= 3 {
			glog.V(logger.Error).Infof("compaction table not found")
			return
		}
		lines = lines[3:]

		for j := 0; j < len(counters[i%2]); j++ {
			counters[i%2][j] = 0
		}
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) != 6 {
				break
			}
			for idx, counter := range parts[3:] {
				if value, err := strconv.ParseFloat(strings.TrimSpace(counter), 64); err != nil {
					glog.V(logger.Error).Infof("compaction entry parsing failed: %v", err)
					return
				} else {
					counters[i%2][idx] += value
				}
			}
		}

		if self.compTimeMeter != nil {
			self.compTimeMeter.Mark(int64((counters[i%2][0] - counters[(i-1)%2][0]) * 1000 * 1000 * 1000))
		}
		if self.compReadMeter != nil {
			self.compReadMeter.Mark(int64((counters[i%2][1] - counters[(i-1)%2][1]) * 1024 * 1024))
		}
		if self.compWriteMeter != nil {
			self.compWriteMeter.Mark(int64((counters[i%2][2] - counters[(i-1)%2][2]) * 1024 * 1024))
		}

		select {
		case errc := <-self.quitChan:

			errc <- nil
			return

		case <-time.After(refresh):

		}
	}
}
