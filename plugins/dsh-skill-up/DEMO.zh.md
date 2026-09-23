# DSH 插件用例：让 `code-stats` 排除依赖目录

演示项目有 `README.md`、`main.go`、`util.go` 三个自有文件，以及 `node_modules/demo-lib/index.js` 一个依赖文件。统计项目代码时应得到 **3 个文件**，扩展名只有 `.go` 和 `.md`。以下截图来自隔离的本地演示项目，画面只保留对话内容。

## 1. 调用：发现依赖文件被计入

用户输入：“用 code-stats 统计这个项目的文件和扩展名。”

DSH 调用原版 `code-stats` 后报告 `Total Files: 4`，扩展名表多出 `.js: 1`，最大文件列表还出现了依赖目录中的 `index.js`。

![简短调用输入与错误的 4 个文件统计](../../docs/public/dsh-code-stats-before.jpg)

## 2. 反馈：把错误留作回归线索

用户输入：“刚才把 node_modules 也算进去了。请记下这个问题，并给我看看回归用例草稿。”

插件的本地观察记录确认 `code-stats` 曾通过 DSH 的 `skill` 工具加载，归因方式为 `instrumented`。反馈被记为 `negative`，预览了候选用例；候选状态仍是 `candidate`，预览不会自动写入用例。

![简短反馈输入与已记录反馈的确认](../../docs/public/dsh-code-stats-feedback.jpg)

## 3. 改进：更新扫描规则并重试

用户输入：“把 code-stats 改成默认跳过 node_modules，再统计一次确认。”

隔离演示中的 Skill 加入排除规则后，同一项目的统计变为 `Total Files: 3`，不再出现依赖目录的 `.js`。仓库里的 [`code-stats/SKILL.md`](../../examples/code-stats/SKILL.md) 将规则补全为默认排除 `node_modules/`、`.git/`、`dist/` 和 `build/`；用户明确要求时仍可统计这些目录。

![简短改进输入、Skill 修改说明与 3 个文件的结果](../../docs/public/dsh-code-stats-after.jpg)

## 4. 验证：同一回归用例从失败变为通过

用户输入：“比较 code-stats 回归用例修改前后的评测结果。”

人工补充的 [`exclude-dependencies.yaml`](../../examples/code-stats/evals/cases/exclude-dependencies.yaml) 自行创建上述四个文件，要求统计 `./project`，并断言总文件数为 3、`.go` 为 2、`.md` 为 1、扩展名表中没有 `.js`。原版 Skill 跑出的基线为 **FAIL**（4 个文件）；改进后用同一用例重跑为 **PASS**（3 个文件）。`skill_up_compare` 读取保存的两份报告，确认用例集合相同。

![简短验证输入与同一用例的 FAIL 到 PASS 对比](../../docs/public/dsh-code-stats-compare.jpg)

前后评测各运行一次，图中结果只证明这次实际运行的变化。观察与报告关联的试验中，模型曾自行调用批准工具，因此审批边界尚未得到验证；本例也没有把自动写用例算作演示结果。
