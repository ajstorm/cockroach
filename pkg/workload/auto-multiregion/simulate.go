package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	_ "github.com/lib/pq"
)

type operationType int

const (
	readOperation = iota
	writeOperation
)

// FIXME: this is a huge hack right now. Need a more elegant solution here.
const (
	hq   = -2
	none = -1
)

// To run - go run pkg/workload/auto-multiregion/simulate.go --username=demo --hostname=localhost --port=26257
// Working on query to show global tables. Stopped with this: select sum(reads) as reads, sum(writes) as writes, reads + writes / sum(reads+writes) as regional_afinity  from multi_region_stats where object = 'promo_codes'
// Still need to figure out how to get the max of all of the regional affinity values.
// Then need to build the queries for the other two table patterns and cleanup this mess.
func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	reader := bufio.NewReader(os.Stdin)
	args := os.Args[1:]
	var err error
	var username string
	var port int
	var hostname string
	tables := []string{"rides", "promo_codes", "revenue"}

	// FIXME: This should either be input, or created here (if possible)
	const databaseName = "movr"
	// TODO: Make this an input parameter?
	const numRowsToInsert = 1000

	const usage = "simulate --username:<username> --hostname:<hostname> --port:<port_num> \n"
	if len(os.Args) == 1 {
		fmt.Print(usage)
		return
	}

	for _, arg := range args {
		splitArg := strings.Split(arg, "=")
		if len(splitArg) != 2 {
			fmt.Print(usage)
			return
		}
		switch splitArg[0] {
		case "--username":
			username = splitArg[1]
			//fmt.Printf("username=%s\n", username)
		case "--port":
			port, err = strconv.Atoi(splitArg[1])
			if err != nil {
				fmt.Print("invalid argument %s\n", splitArg)
			}
			//fmt.Printf("port=%d\n", port)
		case "--hostname":
			hostname = splitArg[1]
			//fmt.Printf("hostname=%s\n", hostname)
		}
	}

	if port == 0 || username == "" || hostname == "" {
		fmt.Print("missing one or more required arguments\n")
		fmt.Print(usage)
		return
	}

	fmt.Printf("enter password for user %s\npassword:", username)
	password, err := reader.ReadString('\n')
	if err != nil {
		fmt.Print("invalid password entry")
		return
	}

	password = strings.Replace(password, "\n", "", -1)

	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=require",
		username,
		password,
		hostname,
		port,
		databaseName,
	)

	db, err := sql.Open("postgres", connString)
	if err != nil {
		log.Fatal("error connecting to the database: ", err)
	}

	defer db.Close()

	// Drop table if it exists
	if _, err := db.Exec(
		"DROP TABLE IF EXISTS multi_region_stats"); err != nil {
		log.Fatal(err)
	}

	// Alter database to be multi-region
	rows, err := db.Query("SELECT region FROM [SHOW REGIONS FROM CLUSTER]")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var regions []string

	for rows.Next() {
		var region string
		if err := rows.Scan(&region); err != nil {
			log.Fatal(err)
		}
		regions = append(regions, region)
	}

	if len(regions) == 0 {
		fmt.Printf("not a multi-region cluster\n")
		return
	}

	// FIXME: Break this into setup function
	// Alter database to be multi-region
	stmt := fmt.Sprintf(
		"ALTER DATABASE %s primary region \"%s\"",
		databaseName,
		regions[0],
	)
	if _, err := db.Exec(stmt); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s - success\n", stmt)

	for _, region := range regions[1:] {
		// add remaining regions
		stmt := fmt.Sprintf(
			"alter database %s add region \"%s\"",
			databaseName,
			region,
		)
		if _, err := db.Exec(stmt); err != nil {
			// fixme: eventually handle "already added" and fail the rest of the errors
			// log.Fatal(err)
		}
		fmt.Printf("%s - success\n", stmt)
	}

	// todo: simulate insertion into first table
	// todo: simulate insertion into the row-level tables
	// todo: modify schema to have regional by table table - talk to andy about this

	// Create the "multi_region_stats" table.
	if _, err := db.Exec(
		"CREATE TABLE IF NOT EXISTS multi_region_stats (object string, region string, reads int, writes int, primary key (region, object))"); err != nil {
		log.Fatal(err)
	}

	// Create a tracking table for each table which tracks row level operations.
	for _, table := range tables {
		dtStmt := fmt.Sprintf(
			"drop table if exists \"_%s_mr_stats\"",
			table,
		)

		if _, err := db.Exec(dtStmt); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s - success\n", dtStmt)

		// IRL, this table will have a id column which will reference the row in the
		// base table. Here, for now, we generate an id.
		// FIXME: need to model updates
		ctStmt := fmt.Sprintf(
			"create table if not exists \"_%s_mr_stats\" (id UUID NOT NULL DEFAULT "+
				"gen_random_uuid(), region string, gateway_region string, reads int, "+
				"writes int, primary key (id, region))",
			table,
		)

		if _, err := db.Exec(ctStmt); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s - success\n", ctStmt)
	}

	// fixme: split this into a separate function

	// todo: this logic is pretty simple now.  some future things we could add:
	// - the notion of a transaction, and some number of reads/writes per transaction
	// - more tables
	// - table interactions
	// - concurrent access to tables

	// todo: add regional affinity at row level

	// FIXME: Number of operations should be permameterized
	numOperations := 1000

	// The percentage with which we'll write to a given table.  These must sum
	// to 1.0
	// FIXME: Add validator that they sum to 1.0

	const percent_writes float64 = 0.2
	// Rationale for table write affinities:
	// rides will be written to almost always from the gateway_region
	// promo_codes will be written to from a single region (HQ) but will be read from everywhere
	// revenue will only be written in a single region (HQ) (though it will have to read data from other regions - not yet modeled)
	regional_write_to_affinity := []float64{0.95, 1.0, 1.0}
	// Rationale for table read affinities:
	// rides will be read almost always from the gateway_region
	// promo_codes will be read uniformly from all regions
	// revenue will only be read from a single region
	regional_read_from_affinity := []float64{0.95, 0.0, 1.0}
	// Rationale for table_read_probability:
	// While there could be a promo_codes read with every transaction, it's more
	// likely that they will occur every N transactions (N=10?).  Ride data
	// will have to be read regularly for billing, and periodically for analytics.
	affinitized_write_region := []int{none, hq, hq}
	affinitized_read_region := []int{none, none, hq}
	table_read_probability := []float64{0.5, 0.95, 1.0}
	table_write_probability := []float64{0.9, 0.98, 1.0}

	for i := 0; i < numOperations; i++ {
		//		var table string
		//		var tableNumber int

		// Determine if we're a read or write operation.
		if fo := rand.Float64(); fo < percent_writes {
			// We're writing
			tableNum, region := processTableLevelOperation(
				db,
				tables,
				regions,
				table_write_probability,
				affinitized_write_region,
				writeOperation)

			processRowLevelOperation(
				db,
				tables,
				regions,
				tableNum,
				region,
				regional_write_to_affinity,
				writeOperation,
			)
		} else {
			// We're reading
			tableNum, region := processTableLevelOperation(
				db,
				tables,
				regions,
				table_read_probability,
				affinitized_read_region,
				readOperation)

			processRowLevelOperation(
				db,
				tables,
				regions,
				tableNum,
				region,
				regional_read_from_affinity,
				readOperation,
			)
		}
	}
}

func getRandomRegion(regions []string) (region string) {
	fr := rand.Float64()
	j := 0
	for i := 1.0 / float64(len(regions)); i <= 1.0; i += 1 / float64(len(regions)) {
		if fr < i {
			region = regions[j]
			break
		}
		j++
	}

	// FIXME: make this return something nicer?
	if region == "" {
		log.Fatal("invalid region")
	}

	return region
}

func processRowLevelOperation(
	db *sql.DB,
	tables []string,
	regions []string,
	tableNumber int,
	gatewayRegion string,
	affinity []float64,
	operation operationType,
) {
	var opRegion string
	// Determine whether or not to complete this operation in the gateway region.
	ft := rand.Float64()
	switch {
	case ft < affinity[tableNumber]:
		// We're reading/writing to the gateway region
		opRegion = gatewayRegion
	default:
		// We need to find another region to act on
		opRegion = getRandomRegion(regions)
	}

	initValReadCol := 0
	initValWriteCol := 0
	var colToUpdate string

	switch operation {
	case writeOperation:
		colToUpdate = "writes"
		initValWriteCol = 1
	case readOperation:
		colToUpdate = "reads"
		initValReadCol = 1
	}

	var insertStmt string
	table := fmt.Sprintf("\"_%s_mr_stats\"", tables[tableNumber])
	if opRegion != gatewayRegion {
		gatewayRegion = "NULL"
	}

	// FIXME: Make these tables take default values for the reads/writes
	if operation == writeOperation {
		insertStmt = fmt.Sprintf("INSERT INTO %s (region, gateway_region, "+
			"reads, writes) VALUES ('%s', '%s', %d, %d) "+
			// FIXME: should conflict consider gateway_region here?
			"ON CONFLICT (id, region) DO UPDATE SET %s = %s.%s + 1",
			table,
			opRegion,
			gatewayRegion,
			initValReadCol,
			initValWriteCol,
			colToUpdate,
			table,
			colToUpdate,
		)
		if _, err := db.Exec(insertStmt); err != nil {
			log.Fatal(err)
		}
	} else {
		// TODO: Right now the read path implicitly assumes that all reads are in
		// the same region as the row was written.  We could introduce another level
		// of modeling where we consider the region from which a given row was read.
		var idToRead uuid.UUID
		query := fmt.Sprintf("SELECT id FROM %s ORDER BY RANDOM() LIMIT 1", table)
		rows, err := db.Query(query)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		if foundRow := rows.Next(); !foundRow {
			// Table is empty.  No-op this read until we have some writes to the table.
			return
		}

		if err := rows.Scan(&idToRead); err != nil {
			log.Fatal(err)
		}

		updateStmt := fmt.Sprintf("UPDATE %s set %s = %s + 1 WHERE id = '%s'",
			table,
			colToUpdate,
			colToUpdate,
			idToRead.String(),
		)
		if _, err := db.Exec(updateStmt); err != nil {
			log.Fatalf("error %s encountered while processing statement %s",
				err.Error(),
				updateStmt)
		}
	}
}

// TODO: For reads, randomly select a row in the table, and a corresponding region, and increment it's access count

func processTableLevelOperation(
	db *sql.DB,
	tables []string,
	regions []string,
	accessProb []float64,
	tableRegionAffinity []int,
	operation operationType,
) (tableNumber int, region string) {
	var table string

	// FIXME: Another huge hack - hqRegion is assumed to be first region in regions slice
	hqRegion := regions[0]

	// Pick a table on which to perform the operation
	ft := rand.Float64()
	switch {
	case ft < accessProb[0]:
		table = tables[0]
		tableNumber = 0
	case ft < accessProb[1]:
		table = tables[1]
		tableNumber = 1
	case ft < accessProb[2]:
		table = tables[2]
		tableNumber = 2
	default:
		log.Fatal("Invalid table_write_probability")
	}

	// Pick a region for the given operation (based on affinity for given table)
	switch tableRegionAffinity[tableNumber] {
	case none:
		region = getRandomRegion(regions)
	case hq:
		region = hqRegion
	default:
		region = regions[tableRegionAffinity[tableNumber]]
	}

	var colToUpdate string
	initValReadCol := 0
	initValWriteCol := 0

	switch operation {
	case writeOperation:
		colToUpdate = "writes"
		initValWriteCol = 1
	case readOperation:
		colToUpdate = "reads"
		initValReadCol = 1
	}

	insertStmt := fmt.Sprintf(
		"INSERT INTO multi_region_stats VALUES ('%s', '%s', %d, %d) "+
			"ON CONFLICT (object, region) DO UPDATE SET %s = multi_region_stats.%s + 1",
		table,
		region,
		initValReadCol,
		initValWriteCol,
		colToUpdate,
		colToUpdate,
	)

	if _, err := db.Exec(insertStmt); err != nil {
		log.Fatal(err)
	}

	return tableNumber, region
}
