# 旧 ITSM 主数据迁移至新 ITSM 操作指南与 SOP

> **文档版本**：v2.0  
> **更新日期**：2026-08-19  
> **适用场景**：从旧 ITSM (Gazellio/KEAS) 迁移组织架构（行政部门、仓库/物流节点、地域划分）及全量用户主数据至新 ITSM (`itsm-backend`)。

---

## 1. 概述与现状分析

### 1.1 源系统 (`kaf-main` / Gazellio ITSM) 提取特征
- **部门数据源**：`data/itsm_departments.json`（通过 `/sysDepartment/list.do` 提取 5,272 条节点）。
- **用户数据源**：`data/itsm_users.json`（通过 `/sysUser/list.do` 在线提取 14,393 条记录）。
- **核心关联维度**：
  - 用户工号 `userName` -> `User.username`（唯一）
  - 用户邮箱 `email` -> `User.email`（唯一，重复追加后缀防冲突）
  - 部门关联 `departmentId` -> `Department.code` -> `User.department_id`（自动外键绑定）

### 1.2 新系统 (`itsm-backend`) 结构扩展
- 新系统采用 **Go + Ent ORM + PostgreSQL**。
- `Department` 扩展了 `area_name`（地域）和 `org_type`（区分 `department` 部门 vs `warehouse` 仓库）。
- `User` 自动计算并使用 bcrypt 进行初始密码加盐加密（默认密码 `P@ssw0rd2026!`）。

---

## 2. 数据库扩展与准备 (Pre-requisites)

### Step 1: 扩展 Ent Schema 声明
在 `itsm-backend/ent/schema/department.go` 中添加 `area_name` 与 `org_type` 字段：

```go
// in ent/schema/department.go
field.String("area_name").Comment("区域/地域名称").Optional(),
field.String("org_type").Comment("组织类型: department=行政部门, warehouse=仓库/物流节点").Optional().Default("department"),
```

### Step 2: 执行数据库 DDL
在 PostgreSQL 数据库中添加新增字段（位于 `migrations/20260819_add_department_area_and_org_type.sql`）：

```sql
ALTER TABLE departments ADD COLUMN IF NOT EXISTS area_name VARCHAR(255);
ALTER TABLE departments ADD COLUMN IF NOT EXISTS org_type VARCHAR(255) DEFAULT 'department';
```

---

## 3. 组织架构与仓库迁移 (Departments & Warehouses)

使用迁移工具 `cmd/migrate_legacy_itsm/main.go`：

```bash
cd /home/administrator/project/itsm/itsm-backend

# 1. 编译
go build -o /tmp/migrate_itsm cmd/migrate_legacy_itsm/main.go

# 2. Dry-Run 预演
/tmp/migrate_itsm -dry-run -json /path/to/itsm_departments.json

# 3. 正式落库
/tmp/migrate_itsm -tenant-id 1 -json /path/to/itsm_departments.json
```

---

## 4. 用户主数据提取与迁移 (Users Migration)

### 步骤 1：从旧 ITSM 在线提取全量用户并保存至本地
运行 Python 爬虫 `scripts/fetch_itsm_users.py`：
```bash
python3 /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/scripts/fetch_itsm_users.py
```
*产出*：`/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data/itsm_users.json` (14,393 条用户)。

### 步骤 2：编译与运行用户迁移 CLI
工具位置：`cmd/migrate_legacy_users/main.go`：

```bash
cd /home/administrator/project/itsm/itsm-backend

# 1. 编译
go build -o /tmp/migrate_users cmd/migrate_legacy_users/main.go

# 2. Dry-Run 预演 (过滤 active 用户)
/tmp/migrate_users -dry-run -json /path/to/itsm_users.json

# 3. 正式落库执行
/tmp/migrate_users -tenant-id 1 -json /path/to/itsm_users.json
```

---

## 5. 数据落库验证 (Verification Query)

迁移完成后，通过 `psql` 验证整体落库与数据关联指标：

```bash
psql "postgres://itsm_user:dev123@localhost:5432/itsm?sslmode=disable" -c "
SELECT org_type, COUNT(*) FROM departments GROUP BY org_type;
SELECT COUNT(*) AS total_users FROM users;
SELECT COUNT(*) AS users_with_department FROM users WHERE department_id IS NOT NULL;
"
```

**期望验证结果**：
- **部门/仓库节点**：5,272 条导入（含 246 个仓库节点）。
- **总活跃用户**：9,012 条新增落库。
- **部门绑定关联率**：8,378 名用户成功跨表自动绑定至相应的 `department_id`（绑定率 93%+）。
