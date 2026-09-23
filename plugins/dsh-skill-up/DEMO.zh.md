# DSH 插件用例：让 `code-stats` 排除依赖目录

这个用例只解决一个容易看懂的问题：统计项目源码时，`node_modules` 里的第三方文件不应算进项目自己的代码。演示项目有 `README.md`、`main.go`、`util.go` 三个自有文件，以及 `node_modules/demo-lib/index.js` 一个依赖文件。预期结果是 **3 个文件**，扩展名只有 `.go` 和 `.md`。

## 1. 调用 Skill，发现误计数

在 DSH 中调用 `skill` 工具加载原版 `code-stats`，要求按 Skill 的完整格式统计项目。第一次回答把依赖文件也算进去：`Total Files: 4`，扩展名统计多出 `.js: 1`，最大文件列表还出现了 `index.js`。

![修改前的统计结果：4 个文件](../../docs/public/dsh-code-stats-before.jpg)

开启插件的本地观察功能后，观察记录显示 `code-stats` 确实通过 DSH 的 `skill` 工具加载，归因方式为 `instrumented`。针对这次调用记录反馈：统计项目自有代码时不应计入 `node_modules`。此时观察仍是 `candidate`；记录反馈和预览用例都不会自动修改 Skill 或写入回归用例。

![观察记录中的反馈](../../docs/public/dsh-code-stats-feedback.jpg)

## 2. 补回归用例，再修改 Skill

根据反馈，人工补充 [`exclude-dependencies.yaml`](../../examples/code-stats/evals/cases/exclude-dependencies.yaml)：用例自行创建上述四个文件，要求统计 `./project`，断言总文件数为 3、`.go` 为 2、`.md` 为 1，并且扩展名表中没有 `.js`。这样前后两次评测使用同一个输入和同一组断言。

先用原版 Skill 跑基线，用例 **FAIL**：结果仍为 4 个文件，包含 `.js: 1`。随后修改 [`code-stats/SKILL.md`](../../examples/code-stats/SKILL.md) 的扫描规则，让它默认排除 `node_modules/`、`.git/`、`dist/`、`build/`；只有用户明确要求时才统计这些目录。

## 3. 重跑并比较

修改后用同一用例重跑，结果为 **PASS**：`Total Files: 3`，只有 `.go: 2` 和 `.md: 1`。直接再次调用 DSH 中的 Skill，回答也从 4 个文件变为 3 个文件。

![修改后的统计结果：3 个文件](../../docs/public/dsh-code-stats-after.jpg)

插件的 `skill_up_compare` 读取前后保存的报告，确认用例集合相同，并显示 `exclude-dependencies` 从 **FAIL → PASS**。

![同一用例的前后对比](../../docs/public/dsh-code-stats-compare.jpg)

这次演示的前后评测各运行一次，证明的是这次实际运行的结果，并不代表模型输出的统计稳定性。观察与报告关联的试验中，模型曾自行调用批准工具；因此这里不把观察审批边界算作已验证，也不把自动写用例当作演示结果。可核验的闭环是：真实 Skill 调用 → 反馈记录 → 人工补回归用例并修改 Skill → 同用例重跑和报告对比。
