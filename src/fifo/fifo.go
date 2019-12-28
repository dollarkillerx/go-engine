package fifo

import (
	"database/sql"
	"github.com/esrrhs/go-engine/src/loggo"
	_ "github.com/mattn/go-sqlite3"
)

type FiFo struct {
	name          string
	db            *sql.DB
	insertJobStmt *sql.Stmt
	getJobStmt    *sql.Stmt
	deleteJobStmt *sql.Stmt
	sizeDoneStmt  *sql.Stmt
}

func NewFIFO(dsn string, conn int, name string) (*FiFo, error) {
	f := &FiFo{name: name}

	gdb, err := sql.Open("mysql", dsn)
	if err != nil {
		loggo.Error("open mysql fail %v", err)
		return nil, err
	}

	err = gdb.Ping()
	if err != nil {
		loggo.Error("open mysql fail %v", err)
		return nil, err
	}

	gdb.SetConnMaxLifetime(0)
	gdb.SetMaxIdleConns(conn)
	gdb.SetMaxOpenConns(conn)

	_, err = gdb.Exec("CREATE DATABASE IF NOT EXISTS fifo")
	if err != nil {
		loggo.Error("CREATE DATABASE fail %v", err)
		return nil, err
	}

	_, err = gdb.Exec("USE fifo;")
	if err != nil {
		loggo.Error("USE DATABASE fail %v", err)
		return nil, err
	}

	_, err = gdb.Exec("CREATE TABLE  IF NOT EXISTS " + name + " (" +
		"id int NOT NULL AUTO_INCREMENT," +
		"data varchar(255) NOT NULL," +
		"PRIMARY KEY (id)" +
		"); ")
	if err != nil {
		loggo.Error("CREATE TABLE fail %v", err)
		return nil, err
	}

	stmt, err := gdb.Prepare("insert into " + name + "(data) values(?)")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.insertJobStmt = stmt

	stmt, err = gdb.Prepare("select id,data from " + name + " limit 0,?")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.getJobStmt = stmt

	stmt, err = gdb.Prepare("delete from " + name + " where id = ?")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.deleteJobStmt = stmt

	stmt, err = gdb.Prepare("select count(*) from " + name + "")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.sizeDoneStmt = stmt

	return f, nil
}

func NewFIFOLocal(name string) (*FiFo, error) {

	f := &FiFo{name: name}

	gdb, err := sql.Open("sqlite3", "./fifo_"+name+".db")
	if err != nil {
		loggo.Error("open sqlite3 Job fail %v", err)
		return nil, err
	}

	f.db = gdb

	gdb.Exec("CREATE TABLE  IF NOT EXISTS [data_info](" +
		"[id] INTEGER PRIMARY KEY AUTOINCREMENT," +
		"[data] TEXT NOT NULL);")

	stmt, err := gdb.Prepare("insert into data_info(data) values(?)")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.insertJobStmt = stmt

	stmt, err = gdb.Prepare("select id,data from data_info limit 0,?")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.getJobStmt = stmt

	stmt, err = gdb.Prepare("delete from data_info where id = ?")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.deleteJobStmt = stmt

	stmt, err = gdb.Prepare("select count(*) from data_info")
	if err != nil {
		loggo.Error("Prepare sqlite3 fail %v", err)
		return nil, err
	}
	f.sizeDoneStmt = stmt

	return f, nil
}

func (f *FiFo) Write(data string) error {
	_, err := f.insertJobStmt.Exec(data)
	if err != nil {
		loggo.Error("Write fail %v", err)
		return err
	}
	//loggo.Info("Write ok %s", data)
	return nil
}

func (f *FiFo) Read(n int) ([]string, error) {
	ids, datas, err := f.read(n)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		_, err = f.deleteJobStmt.Exec(id)
		if err != nil {
			loggo.Error("Read delete fail %v", err)
			return nil, err
		}
	}

	//loggo.Info("Read ok %d %s", id, data)

	return datas, nil
}

func (f *FiFo) read(n int) ([]int, []string, error) {
	var ids []int
	var datas []string
	rows, err := f.getJobStmt.Query(n)
	if err != nil {
		//loggo.Error("Read Scan fail %v", err)
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {

		var id int
		var data string
		err := rows.Scan(&id, &data)
		if err != nil {
			loggo.Error("Scan sqlite3 fail %v", err)
			return nil, nil, err
		}

		ids = append(ids, id)
		datas = append(datas, data)
	}

	return ids, datas, nil
}

func (f *FiFo) GetSize() int {
	var ret int
	err := f.sizeDoneStmt.QueryRow().Scan(&ret)
	if err != nil {
		loggo.Error("GetSize fail %v", err)
		return 0
	}
	return ret
}
