package planbuilder

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stackql/stackql/internal/stackql/astvisit"
	"github.com/stackql/stackql/internal/stackql/dto"
	"github.com/stackql/stackql/internal/stackql/handler"
	"github.com/stackql/stackql/internal/stackql/iqlerror"
	"github.com/stackql/stackql/internal/stackql/parse"
	"github.com/stackql/stackql/internal/stackql/parserutil"
	"github.com/stackql/stackql/internal/stackql/plan"
	"github.com/stackql/stackql/internal/stackql/primitive"
	"github.com/stackql/stackql/internal/stackql/primitivebuilder"
	"github.com/stackql/stackql/internal/stackql/primitivegraph"
	"github.com/stackql/stackql/internal/stackql/util"

	"vitess.io/vitess/go/vt/sqlparser"

	log "github.com/sirupsen/logrus"
)

var (
	// only string "false" will disable
	PlanCacheEnabled string = "true"
)

func isPlanCacheEnabled() bool {
	return strings.ToLower(PlanCacheEnabled) != "false"
}

type PlanBuilderInput struct {
	handlerCtx      *handler.HandlerContext
	stmt            sqlparser.SQLNode
	assignedAliases parserutil.TableExprMap
	tables          sqlparser.TableExprs
}

func NewPlanBuilderInput(
	handlerCtx *handler.HandlerContext,
	stmt sqlparser.SQLNode,
	tables sqlparser.TableExprs,
	assignedAliases parserutil.TableExprMap) PlanBuilderInput {
	rv := PlanBuilderInput{
		handlerCtx:      handlerCtx,
		stmt:            stmt,
		tables:          tables,
		assignedAliases: assignedAliases,
	}
	if rv.assignedAliases == nil {
		rv.assignedAliases = make(map[sqlparser.TableName]sqlparser.TableExpr)
	}
	return rv
}

func (pbi PlanBuilderInput) GetStatement() sqlparser.SQLNode {
	return pbi.stmt
}

func (pbi PlanBuilderInput) GetAssignedAliases() map[sqlparser.TableName]sqlparser.TableExpr {
	return pbi.assignedAliases
}

func (pbi PlanBuilderInput) GetTableExprs() sqlparser.TableExprs {
	return pbi.tables
}

func (pbi PlanBuilderInput) GetAuth() (*sqlparser.Auth, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Auth)
	return rv, ok
}

func (pbi PlanBuilderInput) GetAuthRevoke() (*sqlparser.AuthRevoke, bool) {
	rv, ok := pbi.stmt.(*sqlparser.AuthRevoke)
	return rv, ok
}

func (pbi PlanBuilderInput) GetDelete() (*sqlparser.Delete, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Delete)
	return rv, ok
}

func (pbi PlanBuilderInput) GetDescribeTable() (*sqlparser.DescribeTable, bool) {
	rv, ok := pbi.stmt.(*sqlparser.DescribeTable)
	return rv, ok
}

func (pbi PlanBuilderInput) GetExec() (*sqlparser.Exec, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Exec)
	return rv, ok
}

func (pbi PlanBuilderInput) GetInsert() (*sqlparser.Insert, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Insert)
	return rv, ok
}

func (pbi PlanBuilderInput) GetRegistry() (*sqlparser.Registry, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Registry)
	return rv, ok
}

func (pbi PlanBuilderInput) GetSelect() (*sqlparser.Select, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Select)
	return rv, ok
}

func (pbi PlanBuilderInput) GetShow() (*sqlparser.Show, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Show)
	return rv, ok
}

func (pbi PlanBuilderInput) GetSleep() (*sqlparser.Sleep, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Sleep)
	return rv, ok
}

func (pbi PlanBuilderInput) GetUnion() (*sqlparser.Union, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Union)
	return rv, ok
}

func (pbi PlanBuilderInput) GetUse() (*sqlparser.Use, bool) {
	rv, ok := pbi.stmt.(*sqlparser.Use)
	return rv, ok
}

func (pbi PlanBuilderInput) GetHandlerCtx() *handler.HandlerContext {
	return pbi.handlerCtx
}

type planGraphBuilder struct {
	planGraph *primitivegraph.PrimitiveGraph
}

func newPlanGraphBuilder() *planGraphBuilder {
	return &planGraphBuilder{
		planGraph: primitivegraph.NewPrimitiveGraph(),
	}
}

func (pgb *planGraphBuilder) createInstructionFor(pbi PlanBuilderInput) error {
	stmt := pbi.GetStatement()
	switch stmt := stmt.(type) {
	case *sqlparser.Auth:
		return pgb.handleAuth(pbi)
	case *sqlparser.AuthRevoke:
		return pgb.handleAuthRevoke(pbi)
	case *sqlparser.Begin:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: BEGIN")
	case *sqlparser.Commit:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: COMMIT")
	case *sqlparser.DBDDL:
		return iqlerror.GetStatementNotSupportedError(fmt.Sprintf("unsupported: Database DDL %v", sqlparser.String(stmt)))
	case *sqlparser.DDL:
		return iqlerror.GetStatementNotSupportedError("DDL")
	case *sqlparser.Delete:
		return pgb.handleDelete(pbi)
	case *sqlparser.DescribeTable:
		return pgb.handleDescribe(pbi)
	case *sqlparser.Exec:
		return pgb.handleExec(pbi)
	case *sqlparser.Explain:
		return iqlerror.GetStatementNotSupportedError("EXPLAIN")
	case *sqlparser.Insert:
		return pgb.handleInsert(pbi)
	case *sqlparser.OtherRead, *sqlparser.OtherAdmin:
		return iqlerror.GetStatementNotSupportedError("OTHER")
	case *sqlparser.Registry:
		return pgb.handleRegistry(pbi)
	case *sqlparser.Rollback:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: ROLLBACK")
	case *sqlparser.Savepoint:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: SAVEPOINT")
	case *sqlparser.Select:
		_, _, err := pgb.handleSelect(pbi)
		return err
	case *sqlparser.Set:
		return iqlerror.GetStatementNotSupportedError("SET")
	case *sqlparser.SetTransaction:
		return iqlerror.GetStatementNotSupportedError("SET TRANSACTION")
	case *sqlparser.Show:
		return pgb.handleShow(pbi)
	case *sqlparser.Sleep:
		return pgb.handleSleep(pbi)
	case *sqlparser.SRollback:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: SROLLBACK")
	case *sqlparser.Release:
		return iqlerror.GetStatementNotSupportedError("TRANSACTION: RELEASE")
	case *sqlparser.Union:
		_, _, err := pgb.handleUnion(pbi)
		return err
	case *sqlparser.Update:
		return iqlerror.GetStatementNotSupportedError("UPDATE")
	case *sqlparser.Use:
		return pgb.handleUse(pbi)
	}
	return iqlerror.GetStatementNotSupportedError(fmt.Sprintf("BUG: unexpected statement type: %T", stmt))
}

func (pgb *planGraphBuilder) handleAuth(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetAuth()
	if !ok {
		return fmt.Errorf("could not cast node of type '%T' to required Auth", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	prov, err := handlerCtx.GetProvider(node.Provider)
	if err != nil {
		return err
	}
	err = primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		log.Debugln(fmt.Sprintf("err = %s", err.Error()))
		return err
	}
	authCtx, authErr := handlerCtx.GetAuthContext(node.Provider)
	if authErr != nil {
		return authErr
	}
	pr := primitivebuilder.NewMetaDataPrimitive(
		prov,
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			authType := strings.ToLower(node.Type)
			if node.KeyFilePath != "" {
				authCtx.KeyFilePath = node.KeyFilePath
			}
			if node.KeyEnvVar != "" {
				authCtx.KeyEnvVar = node.KeyEnvVar
			}
			_, err := prov.Auth(authCtx, authType, true)
			return dto.NewExecutorOutput(nil, nil, nil, nil, err)
		})
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func (pgb *planGraphBuilder) handleAuthRevoke(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	stmt := pbi.GetStatement()
	node, ok := stmt.(*sqlparser.AuthRevoke)
	if !ok {
		return fmt.Errorf("could not cast statement of type '%T' to required AuthRevoke", stmt)
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		return err
	}
	prov, err := handlerCtx.GetProvider(node.Provider)
	if err != nil {
		return err
	}
	authCtx, authErr := handlerCtx.GetAuthContext(node.Provider)
	if authErr != nil {
		return authErr
	}
	pr := primitivebuilder.NewMetaDataPrimitive(
		prov,
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			return dto.NewExecutorOutput(nil, nil, nil, nil, prov.AuthRevoke(authCtx))
		})
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func (pgb *planGraphBuilder) handleDescribe(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetDescribeTable()
	if !ok {
		return fmt.Errorf("could not cast node of type '%T' to required DescribeTable", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		return err
	}
	md, err := primitiveGenerator.PrimitiveBuilder.GetTable(node)
	if err != nil {
		return err
	}
	prov, err := md.GetProvider()
	if err != nil {
		return err
	}
	var extended bool = strings.TrimSpace(strings.ToUpper(node.Extended)) == "EXTENDED"
	var full bool = strings.TrimSpace(strings.ToUpper(node.Full)) == "FULL"
	pr := primitivebuilder.NewMetaDataPrimitive(
		prov,
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			return primitiveGenerator.describeInstructionExecutor(handlerCtx, md, extended, full)
		})
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func (pgb *planGraphBuilder) handleSelect(pbi PlanBuilderInput) (*primitivegraph.PrimitiveNode, *primitivegraph.PrimitiveNode, error) {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetSelect()
	if !ok {
		return nil, nil, fmt.Errorf("could not cast statement of type '%T' to required Select", pbi.GetStatement())
	}
	if !handlerCtx.RuntimeContext.TestWithoutApiCalls {
		primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
		err := primitiveGenerator.analyzeStatement(pbi)
		if err != nil {
			log.Infoln(fmt.Sprintf("select statement analysis error = '%s'", err.Error()))
			return nil, nil, err
		}
		isLocallyExecutable := true
		for _, val := range primitiveGenerator.PrimitiveBuilder.GetTables() {
			isLocallyExecutable = isLocallyExecutable && val.IsLocallyExecutable
		}
		if isLocallyExecutable {
			pr, err := primitiveGenerator.localSelectExecutor(handlerCtx, node, util.DefaultRowSort)
			if err != nil {
				return nil, nil, err
			}
			rv := pgb.planGraph.CreatePrimitiveNode(pr)
			return &rv, &rv, nil
		}
		if primitiveGenerator.PrimitiveBuilder.GetBuilder() == nil {
			return nil, nil, fmt.Errorf("builder not created for select, cannot proceed")
		}
		builder := primitiveGenerator.PrimitiveBuilder.GetBuilder()
		err = builder.Build()
		if err != nil {
			return nil, nil, err
		}
		root := builder.GetRoot()
		tail := builder.GetTail()
		return &root, &tail, nil
	}
	pr := primitivebuilder.NewLocalPrimitive(nil)
	rv := pgb.planGraph.CreatePrimitiveNode(pr)
	return &rv, &rv, nil
}

func (pgb *planGraphBuilder) handleUnion(pbi PlanBuilderInput) (*primitivegraph.PrimitiveNode, *primitivegraph.PrimitiveNode, error) {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetUnion()
	if !ok {
		return nil, nil, fmt.Errorf("could not cast node of type '%T' to required Delete", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		log.Infoln(fmt.Sprintf("select statement analysis error = '%s'", err.Error()))
		return nil, nil, err
	}
	isLocallyExecutable := true
	for _, val := range primitiveGenerator.PrimitiveBuilder.GetTables() {
		isLocallyExecutable = isLocallyExecutable && val.IsLocallyExecutable
	}
	if primitiveGenerator.PrimitiveBuilder.GetBuilder() == nil {
		return nil, nil, fmt.Errorf("builder not created for union, cannot proceed")
	}
	builder := primitiveGenerator.PrimitiveBuilder.GetBuilder()
	err = builder.Build()
	if err != nil {
		return nil, nil, err
	}
	root := builder.GetRoot()
	tail := builder.GetTail()
	return &root, &tail, nil
}

func (pgb *planGraphBuilder) handleDelete(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetDelete()
	if !ok {
		return fmt.Errorf("could not cast node of type '%T' to required Delete", pbi.GetStatement())
	}
	if !handlerCtx.RuntimeContext.TestWithoutApiCalls {
		primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
		err := primitiveGenerator.analyzeStatement(pbi)
		if err != nil {
			return err
		}
		pr, err := primitiveGenerator.deleteExecutor(handlerCtx, node)
		if err != nil {
			return err
		}
		pgb.planGraph.CreatePrimitiveNode(pr)
		return nil
	} else {
		pr := primitivebuilder.NewHTTPRestPrimitive(nil, nil, nil, nil)
		pgb.planGraph.CreatePrimitiveNode(pr)
		return nil
	}
	return nil
}

func (pgb *planGraphBuilder) handleRegistry(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetRegistry()
	if !ok {
		return fmt.Errorf("could not cast statement of type '%T' to required Registry", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeRegistry(pbi)
	if err != nil {
		return err
	}
	reg, err := handler.GetRegistry(handlerCtx.RuntimeContext)
	if err != nil {
		return err
	}
	pr := primitivebuilder.NewLocalPrimitive(
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			switch at := strings.ToLower(node.ActionType); at {
			case "pull":
				err := reg.PullAndPersistProviderArchive(node.ProviderId, node.ProviderVersion)
				if err != nil {
					return dto.NewErroneousExecutorOutput(err)
				}
				return util.PrepareResultSet(dto.NewPrepareResultSetPlusRawDTO(nil, nil, nil, nil, nil, &dto.BackendMessages{WorkingMessages: []string{fmt.Sprintf("%s provider, version '%s' successfully installed", node.ProviderId, node.ProviderVersion)}}, nil))
			case "list":
				provz, err := reg.ListAllAvailableProviders()
				if err != nil {
					return dto.NewErroneousExecutorOutput(err)
				}
				colz := []string{"provider", "version"}
				keys := make(map[string]map[string]interface{})
				i := 0
				for k, v := range provz {
					for _, ver := range v.Versions {
						keys[strconv.Itoa(i)] = map[string]interface{}{
							"provider": k,
							"version":  ver,
						}
						i++
					}
				}
				return util.PrepareResultSet(dto.NewPrepareResultSetPlusRawDTO(nil, keys, colz, nil, nil, nil, nil))
			default:
				return dto.NewErroneousExecutorOutput(fmt.Errorf("registry action '%s' no supported", at))
			}
		},
	)
	pgb.planGraph.CreatePrimitiveNode(pr)

	return nil
}

func (pgb *planGraphBuilder) handleInsert(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetInsert()
	if !ok {
		return fmt.Errorf("could not cast statement of type '%T' to required Insert", pbi.GetStatement())
	}
	if !handlerCtx.RuntimeContext.TestWithoutApiCalls {
		primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
		err := primitiveGenerator.analyzeInsert(pbi)
		if err != nil {
			return err
		}
		insertValOnlyRows, nonValCols, err := parserutil.ExtractInsertValColumns(node)
		if err != nil {
			return err
		}
		var selectPrimitive primitive.IPrimitive
		var selectPrimitiveNode *primitivegraph.PrimitiveNode
		if nonValCols > 0 {
			switch rowsNode := node.Rows.(type) {
			case *sqlparser.Select:
				selPbi := NewPlanBuilderInput(pbi.GetHandlerCtx(), rowsNode, pbi.GetTableExprs(), pbi.GetAssignedAliases())
				_, selectPrimitiveNode, err = pgb.handleSelect(selPbi)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("insert with rows of type '%T' not currently supported", rowsNode)
			}
		} else {
			selectPrimitive, err = primitiveGenerator.insertableValsExecutor(handlerCtx, insertValOnlyRows)
			if err != nil {
				return err
			}
			sn := pgb.planGraph.CreatePrimitiveNode(selectPrimitive)
			selectPrimitiveNode = &sn
		}
		pr, err := primitiveGenerator.insertExecutor(handlerCtx, node, util.DefaultRowSort)
		if err != nil {
			return err
		}
		if selectPrimitiveNode == nil {
			return fmt.Errorf("nil selection for insert -- cannot work")
		}
		pr.SetInputAlias("", selectPrimitiveNode.ID())
		prNode := pgb.planGraph.CreatePrimitiveNode(pr)
		pgb.planGraph.NewDependency(*selectPrimitiveNode, prNode, 1.0)
		return nil
	} else {
		pr := primitivebuilder.NewHTTPRestPrimitive(nil, nil, nil, nil)
		pgb.planGraph.CreatePrimitiveNode(pr)
		return nil
	}
	return nil
}

func (pgb *planGraphBuilder) handleExec(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetExec()
	if !ok {
		return fmt.Errorf("could not cast node of type '%T' to required Exec", pbi.GetStatement())
	}
	if !handlerCtx.RuntimeContext.TestWithoutApiCalls {
		primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
		err := primitiveGenerator.analyzeStatement(pbi)
		if err != nil {
			return err
		}
		_, err = primitiveGenerator.execExecutor(handlerCtx, node)
		if err != nil {
			return err
		}
		return nil
	}
	pr := primitivebuilder.NewHTTPRestPrimitive(nil, nil, nil, nil)
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func (pgb *planGraphBuilder) handleShow(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetShow()
	if !ok {
		return fmt.Errorf("could not cast statement of type '%T' to required Show", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		return err
	}
	pr := primitivebuilder.NewMetaDataPrimitive(
		primitiveGenerator.PrimitiveBuilder.GetProvider(),
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			return primitiveGenerator.showInstructionExecutor(node, handlerCtx)
		})
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func (pgb *planGraphBuilder) handleSleep(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetSleep()
	if !ok {
		return fmt.Errorf("could not cast statement of type '%T' to required Sleep", pbi.GetStatement())
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		return err
	}
	return nil
}

func (pgb *planGraphBuilder) handleUse(pbi PlanBuilderInput) error {
	handlerCtx := pbi.GetHandlerCtx()
	node, ok := pbi.GetUse()
	if !ok {
		return fmt.Errorf("node type '%T' is incorrect; expected *Use", node)
	}
	primitiveGenerator := newRootPrimitiveGenerator(node, handlerCtx, pgb.planGraph)
	err := primitiveGenerator.analyzeStatement(pbi)
	if err != nil {
		return err
	}
	pr := primitivebuilder.NewMetaDataPrimitive(
		primitiveGenerator.PrimitiveBuilder.GetProvider(),
		func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
			handlerCtx.CurrentProvider = node.DBName.GetRawVal()
			return dto.NewExecutorOutput(nil, nil, nil, nil, nil)
		})
	pgb.planGraph.CreatePrimitiveNode(pr)
	return nil
}

func createErroneousPlan(handlerCtx *handler.HandlerContext, qPlan *plan.Plan, rowSort func(map[string]map[string]interface{}) []string, err error) (*plan.Plan, error) {
	qPlan.Instructions = primitivebuilder.NewLocalPrimitive(func(pc primitive.IPrimitiveCtx) dto.ExecutorOutput {
		return util.PrepareResultSet(
			dto.PrepareResultSetDTO{
				OutputBody:  nil,
				Msg:         nil,
				RowMap:      nil,
				ColumnOrder: nil,
				RowSort:     rowSort,
				Err:         err,
			},
		)
	})
	return qPlan, err
}

func BuildPlanFromContext(handlerCtx *handler.HandlerContext) (*plan.Plan, error) {
	planKey := handlerCtx.Query
	if qp, ok := handlerCtx.LRUCache.Get(planKey); ok && isPlanCacheEnabled() {
		log.Infoln("retrieving query plan from cache")
		pl, ok := qp.(*plan.Plan)
		if ok {
			pl.Instructions.SetTxnId(handlerCtx.TxnCounterMgr.GetNextTxnId())
			return pl, nil
		}
		return qp.(*plan.Plan), nil
	}
	qPlan := &plan.Plan{
		Original: handlerCtx.RawQuery,
	}
	var err error
	var rowSort func(map[string]map[string]interface{}) []string
	var statement sqlparser.Statement
	statement, err = parse.ParseQuery(handlerCtx.Query)
	if err != nil {
		return createErroneousPlan(handlerCtx, qPlan, rowSort, err)
	}
	s := sqlparser.String(statement)
	result, err := sqlparser.RewriteAST(statement)
	if err != nil {
		return createErroneousPlan(handlerCtx, qPlan, rowSort, err)
	}
	vis := astvisit.NewDRMAstVisitor("iql_query_id", false)
	statement.Accept(vis)
	provStrSlice := astvisit.ExtractProviderStrings(result.AST)
	for _, p := range provStrSlice {
		_, err := handlerCtx.GetProvider(p)
		if err != nil {
			return nil, err
		}
	}
	log.Infoln("Recovered query: " + s)
	log.Infoln("Recovered query from vis: " + vis.GetRewrittenQuery())
	if err != nil {
		return createErroneousPlan(handlerCtx, qPlan, rowSort, err)
	}
	statementType := sqlparser.ASTToStatementType(result.AST)
	if err != nil {
		return createErroneousPlan(handlerCtx, qPlan, rowSort, err)
	}
	qPlan.Type = statementType

	pGBuilder := newPlanGraphBuilder()

	ast := result.AST
	tVis := astvisit.NewTableExtractAstVisitor()
	tVis.Visit(ast)

	aVis := astvisit.NewTableAliasAstVisitor(tVis.GetTables())
	aVis.Visit(ast)

	pbi := NewPlanBuilderInput(handlerCtx, ast, tVis.GetTables(), aVis.GetAliases())

	createInstructionError := pGBuilder.createInstructionFor(pbi)
	if createInstructionError != nil {
		return nil, createInstructionError
	}

	qPlan.Instructions = pGBuilder.planGraph

	if qPlan.Instructions != nil {
		err = qPlan.Instructions.Optimise()
		if err != nil {
			return createErroneousPlan(handlerCtx, qPlan, rowSort, err)
		}
		handlerCtx.LRUCache.Set(planKey, qPlan)
	}

	return qPlan, err
}