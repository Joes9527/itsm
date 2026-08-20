package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"itsm-backend/config"
	"itsm-backend/database"

	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

type EHROrg struct {
	UniqueID   string
	OrgCode    string
	OrgName    string
	ParentCode string
	ParentName string
}

type EHRPerson struct {
	UniqueID   string
	EmpID      string
	EmpName    string
	CnName     string
	Mobile     string
	Email      string
	ADCode     string
	ADEmail    string
	StoreName  string
	StoreCode  string
	DepartCode string
	DepartLine string
	Post       string
	Supervisor string
}

func isWarehouse(name string) bool {
	keywords := []string{"仓", "库", "Warehouse", "DC", "Hub", "储", "物流"}
	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}

func main() {
	excelPathFlag := flag.String("excel", "/mnt/d/SynologyDrive/kerry/service-support/ehr-data.xlsx", "Path to ehr-data.xlsx")
	tenantIDFlag := flag.Int("tenant-id", 1, "Target tenant ID (default: 1)")
	defaultPasswordFlag := flag.String("default-password", "P@ssw0rd2026!", "Default initial password for active users")
	dryRunFlag := flag.Bool("dry-run", false, "Dry run mode without DB writes")
	flag.Parse()

	log.Printf("Loading EHR Excel file from: %s", *excelPathFlag)
	f, err := excelize.OpenFile(*excelPathFlag)
	if err != nil {
		log.Fatalf("Failed to open Excel file: %v", err)
	}
	defer f.Close()

	// -------------------------------------------------------------
	// 1. Read ORG sheet
	// -------------------------------------------------------------
	orgRows, err := f.GetRows("org")
	if err != nil {
		log.Fatalf("Failed to read org sheet: %v", err)
	}
	log.Printf("Total rows in org sheet: %d", len(orgRows))

	var orgs []EHROrg
	for idx, row := range orgRows {
		if idx == 0 || len(row) < 3 {
			continue // skip header
		}
		uniqueID := strings.TrimSpace(row[0])
		orgCode := strings.TrimSpace(row[1])
		orgName := strings.TrimSpace(row[2])

		parentCode := ""
		if len(row) > 3 {
			parentCode = strings.TrimSpace(row[3])
		}
		parentName := ""
		if len(row) > 4 {
			parentName = strings.TrimSpace(row[4])
		}

		if orgCode == "" || orgName == "" {
			continue
		}

		orgs = append(orgs, EHROrg{
			UniqueID:   uniqueID,
			OrgCode:    orgCode,
			OrgName:    orgName,
			ParentCode: parentCode,
			ParentName: parentName,
		})
	}
	log.Printf("Parsed %d valid EHR organization nodes", len(orgs))

	// -------------------------------------------------------------
	// 2. Read Person sheet (Filter USR = 在职)
	// -------------------------------------------------------------
	personRows, err := f.GetRows("person")
	if err != nil {
		log.Fatalf("Failed to read person sheet: %v", err)
	}
	log.Printf("Total rows in person sheet: %d", len(personRows))

	var activePersons []EHRPerson
	for idx, row := range personRows {
		if idx == 0 || len(row) < 3 {
			continue // skip header
		}

		storeName := ""
		if len(row) > 8 {
			storeName = strings.TrimSpace(row[8])
		}

		// Only import active users (USR)
		if storeName != "USR" {
			continue
		}

		empID := strings.TrimSpace(row[1])
		empName := strings.TrimSpace(row[2])
		cnName := ""
		if len(row) > 3 {
			cnName = strings.TrimSpace(row[3])
		}
		mobile := ""
		if len(row) > 4 {
			mobile = strings.TrimSpace(row[4])
		}
		email := ""
		if len(row) > 5 {
			email = strings.TrimSpace(row[5])
		}
		departCode := ""
		if len(row) > 10 {
			departCode = strings.TrimSpace(row[10])
		}
		post := ""
		if len(row) > 12 {
			post = strings.TrimSpace(row[12])
		}
		supervisor := ""
		if len(row) > 13 {
			supervisor = strings.TrimSpace(row[13])
		}

		if empID == "" {
			continue
		}

		activePersons = append(activePersons, EHRPerson{
			EmpID:      empID,
			EmpName:    empName,
			CnName:     cnName,
			Mobile:     mobile,
			Email:      email,
			StoreName:  storeName,
			DepartCode: departCode,
			Post:       post,
			Supervisor: supervisor,
		})
	}
	log.Printf("Parsed %d active (USR) employees from EHR", len(activePersons))

	if *dryRunFlag {
		log.Printf("[DRY RUN] Would import %d departments and %d users. Exiting.", len(orgs), len(activePersons))
		return
	}

	// -------------------------------------------------------------
	// 3. Connect DB & Begin Import
	// -------------------------------------------------------------
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	client, err := database.InitDatabase(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	tenantID := *tenantIDFlag

	// -------------------------------------------------------------
	// 4. Import Departments Tree (Topological Order)
	// -------------------------------------------------------------
	log.Println("Starting EHR Departments Import...")
	codeToEntID := make(map[string]int)
	codeToOrg := make(map[string]EHROrg)
	for _, o := range orgs {
		codeToOrg[o.OrgCode] = o
	}

	remainingOrgs := orgs
	insertedDeptCount := 0

	for len(remainingOrgs) > 0 {
		progress := false
		nextRemaining := make([]EHROrg, 0, len(remainingOrgs))

		for _, o := range remainingOrgs {
			// Treat parent_code = "" / "0" / "root" as root sentinels, and a row whose
			// parent_code equals its own org_code as a self-referencing root (the source
			// data does this for the true top node: org_code=1 has parent_code=1). "1"
			// itself is NOT a sentinel — it's a real, meaningful org_code (公司架构) that
			// other rows legitimately point to as their parent. Special-casing the literal
			// "1" here previously caused every child of that node to be silently imported
			// as a false root instead of nesting under it (fixed via
			// itsm-backend/migrations/20260819_fix_department_parent_link_numeric_codes.sql
			// for data already imported before this fix).
			parentEntID := 0
			hasParent := false
			isSelfReference := o.ParentCode == o.OrgCode

			if o.ParentCode != "" && o.ParentCode != "0" && o.ParentCode != "root" && !isSelfReference {
				if pEntID, ok := codeToEntID[o.ParentCode]; ok {
					parentEntID = pEntID
					hasParent = true
				} else if _, parentExistsInSource := codeToOrg[o.ParentCode]; !parentExistsInSource {
					// Parent does not exist in dataset, treat as root-level child
					hasParent = false
				} else {
					// Parent exists but not yet created -> defer
					nextRemaining = append(nextRemaining, o)
					continue
				}
			}

			orgType := "department"
			if isWarehouse(o.OrgName) {
				orgType = "warehouse"
			}

			c := client.Department.Create().
				SetName(o.OrgName).
				SetCode(o.OrgCode).
				SetDescription(fmt.Sprintf("EHR UniqueID: %s", o.UniqueID)).
				SetTenantID(tenantID).
				SetAreaName("中国").
				SetOrgType(orgType)

			if hasParent && parentEntID > 0 {
				c.SetParentID(parentEntID)
			}

			created, err := c.Save(ctx)
			if err != nil {
				log.Printf("Failed to create department %s (%s): %v", o.OrgName, o.OrgCode, err)
			} else {
				codeToEntID[o.OrgCode] = created.ID
				insertedDeptCount++
			}
			progress = true
		}

		if !progress {
			log.Printf("Warning: Break condition hit with %d unresolvable orphan org nodes. Importing remainder as roots.", len(nextRemaining))
			for _, o := range nextRemaining {
				orgType := "department"
				if isWarehouse(o.OrgName) {
					orgType = "warehouse"
				}
				created, err := client.Department.Create().
					SetName(o.OrgName).
					SetCode(o.OrgCode).
					SetTenantID(tenantID).
					SetAreaName("中国").
					SetOrgType(orgType).
					Save(ctx)
				if err == nil {
					codeToEntID[o.OrgCode] = created.ID
					insertedDeptCount++
				}
			}
			break
		}
		remainingOrgs = nextRemaining
	}
	log.Printf("Department Import Complete! Inserted: %d, Total Map Size: %d", insertedDeptCount, len(codeToEntID))

	// -------------------------------------------------------------
	// 5. Import Active Users & Link Departments
	// -------------------------------------------------------------
	log.Println("Starting EHR Active Users Import...")
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*defaultPasswordFlag), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed to hash default password: %v", err)
	}
	passwordHashStr := string(hashedPassword)

	seenUsernames := make(map[string]bool)
	seenEmails := make(map[string]bool)
	empIDToUserID := make(map[string]int)

	insertedUserCount := 0
	deptLinkedCount := 0

	for idx, p := range activePersons {
		username := p.EmpID
		name := p.EmpName
		if name == "" {
			name = username
		}
		email := strings.TrimSpace(p.Email)
		phone := strings.TrimSpace(p.Mobile)

		if email == "" {
			email = fmt.Sprintf("%s@keas.kln.com", strings.ToLower(username))
		}

		// Handle duplicate email in dataset if any
		origEmail := email
		emailSuffix := 1
		for seenEmails[strings.ToLower(email)] {
			emailSuffix++
			parts := strings.SplitN(origEmail, "@", 2)
			if len(parts) == 2 {
				email = fmt.Sprintf("%s+%d@%s", parts[0], emailSuffix, parts[1])
			} else {
				email = fmt.Sprintf("%s_%d", origEmail, emailSuffix)
			}
		}
		seenEmails[strings.ToLower(email)] = true
		seenUsernames[username] = true

		// Find department ID by depart_code
		var deptID int
		var deptName string
		if p.DepartCode != "" {
			if id, ok := codeToEntID[p.DepartCode]; ok {
				deptID = id
				if orgObj, found := codeToOrg[p.DepartCode]; found {
					deptName = orgObj.OrgName
				}
			}
		}

		c := client.User.Create().
			SetUsername(username).
			SetName(name).
			SetEmail(email).
			SetPhone(phone).
			SetRole("end_user").
			SetPasswordHash(passwordHashStr).
			SetActive(true).
			SetTenantID(tenantID)

		if deptID > 0 {
			c.SetDepartmentID(deptID)
			c.SetDepartment(deptName)
			deptLinkedCount++
		}

		created, err := c.Save(ctx)
		if err != nil {
			log.Printf("[%d/%d] Failed to create user %s (%s): %v", idx+1, len(activePersons), username, email, err)
		} else {
			insertedUserCount++
			empIDToUserID[p.EmpID] = created.ID
		}

		if (idx+1)%1000 == 0 || idx+1 == len(activePersons) {
			log.Printf("User Progress: %d/%d processed (Inserted: %d, Dept Linked: %d)", idx+1, len(activePersons), insertedUserCount, deptLinkedCount)
		}
	}

	log.Printf("EHR Import Finished! Total Org: %d, Total Active Users: %d (Dept Linked: %d)", insertedDeptCount, insertedUserCount, deptLinkedCount)

	// -------------------------------------------------------------
	// 6. Link Direct Supervisors (Second Pass)
	// -------------------------------------------------------------
	// Supervisor is "姓名:工号" (e.g. "徐一晨:D31717") — a person-level reporting line,
	// distinct from the department-level manager set above. Needs a second pass over
	// all users (not folded into the creation loop) because a person's supervisor row
	// can appear anywhere else in the sheet, before or after their own row, so the
	// supervisor's user ID may not exist yet at creation time.
	log.Println("Starting Direct Supervisor Linking...")
	linkedSupervisorCount := 0
	skippedSupervisorCount := 0
	for _, p := range activePersons {
		selfID, ok := empIDToUserID[p.EmpID]
		if !ok || p.Supervisor == "" {
			continue
		}
		sup := strings.TrimSpace(p.Supervisor)
		if strings.ContainsAny(sup, "、,") {
			// Multiple supervisors listed — no single reporting line to pick, skip.
			skippedSupervisorCount++
			continue
		}
		parts := strings.SplitN(sup, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			skippedSupervisorCount++
			continue
		}
		supervisorEmpID := strings.TrimSpace(parts[1])
		managerID, ok := empIDToUserID[supervisorEmpID]
		if !ok || managerID == selfID {
			skippedSupervisorCount++
			continue
		}
		if err := client.User.UpdateOneID(selfID).SetManagerID(managerID).Exec(ctx); err != nil {
			log.Printf("Failed to link supervisor for %s -> %s: %v", p.EmpID, supervisorEmpID, err)
			continue
		}
		linkedSupervisorCount++
	}
	log.Printf("Direct Supervisor Linking Complete! Linked: %d, Skipped: %d", linkedSupervisorCount, skippedSupervisorCount)
}
