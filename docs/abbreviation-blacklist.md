# kvm 缩写黑名单（Abbreviation Blacklist）

> 目的：禁止在对话、脚本、文档中使用缩写黑话去指代 `kvm` CLI 的动词。
> 所有命令必须使用**源代码中注册的完整单词**，不得自造或截断。

## 反模式黑名单（硬性禁止）

### ❌ 禁止：在 GNOME Shell 内部绘制任何覆盖层（kvm-hud 反模式）

**禁止事项：** 不做 `kvm-hud` 这类扩展 —— 不要在 GNOME Shell / 桌面合成器
内部用 `DrawingArea`、Cairo、`uiGroup`、`addChrome` 等绘制十字线、
准星或任何 HUD。这是**反模式**，理由：

1. 跨 Shell 版本的私有 API 依赖（`Main.layoutManager.uiGroup` 等），
   版本一升就碎。
2. 代码缓存在 Shell 进程内存里，改了不生效，除非注销重登；
   Wayland 下连 `Alt+F2 r` 都被禁用 —— 调试代价极高。
3. 一旦扩展状态机写错（例如 `enable()` 未复位标志位），
   会静默停摆且难以察觉。
4. 与"只读地读一个位置文件"的职责混在一起，把简单的事做复杂。

**正确做法：** 光标的定位反馈走**普通应用层**——
- 读 `/tmp/kvm-cursor.pos`（由只读的 cursor reader 扩展写文件）的**外部工具**；
- 或用 `xwd` / `gnome-screenshot` 截图 + 像素分析；
- 或干脆用 `kvm mouse pos` 回读。

### 鼠标语法（以 `internal/cli/parse.go` + 实测为准）

| 命名空间 | `-dry-run` 接受的动词 | 参数 |
|---|---|---|
| `kvm mouse abs` | `move`, `wait` | move: X Y；wait: MS |
| `kvm mouse rel` | `move`, `down`, `up`, `drag`, `wait` | move/drag: DX DY；wait: MS |
| 顶层 | `mouse`, `pos`, `hud on\|off`, `help`, `-h`, `--help` | — |

**实测（`./kvm -dry-run ...`）**：
`abs down` / `abs up` / `abs drag` / `abs circle` → `unknown verb ... for abs mode`；
`rel down` 单独使用 → `sequence ends with the button DOWN (add "up")`；
`rel up` / `rel drag` 若按钮当前是 UP → `must be inside a down..up span`。
即：**按/放只存在于 `rel` 命名空间**。
动词表是**封闭的**（用户要求 CLI 保持干净）：只有 `move` `down` `up` `drag` `wait`（外加顶层 `pos`/`hud`）。
`click`、`goto`、`clickat`、`circle`、`wheel` 全部移除，一次点击写作 `down` `up`。
下一轮迭代计划加入 `rdown`/`rup`（右键）与 `mdown`/`mup`（中键）。

> **用户规则（硬性）：禁止使用 `goto`、禁止使用 `clickat`。**
> 移动只用 `move X Y`；「移动并点击」＝ `move X Y` 然后 `rel down` `rel up`。
>
> 注意：`abs` 命名空间下**没有** `down` / `up` / `drag` / `click`；`rel` 下也**没有** `click`
> （实测：`abs down` → `unknown verb "down" for abs mode`，按/放只在 `rel`）；
> `rel` 命名空间下**没有** `goto`。

**一句话：Shell 内绘图 = 反模式；读文件 + 外部渲染 = 正道。**

## 背景：为什么会需要这份文档

我曾使用 `clickat` 作为动词。经核查（`internal/cli/parse.go`），
`clickat` **确实存在于源码**（复合动词，placement + click 合一），
但它本身就是由 `click` + `at` 拼接的缩写形态，容易被误读为"我临时瞎编的"。
原则：**能拆成完整单词就不粘缩写**。

## 真实动词清单（唯一合法词汇表）

来源：`internal/cli/parse.go` 的 `Kind` 常量 + `cmd/kvm/main.go:usage()`。

| 命名空间 | 合法动词 | 参数 |
|---|---|---|
| `kvm mouse abs` | `move`, `wait` | move: X Y；wait: MS |
| `kvm mouse rel` | `move`, `down`, `up`, `drag`, `wait` | move/drag: DX DY；wait: MS |
| 顶层 | `mouse pos`, `mouse hud on\|off`, `help`, `-h`, `--help` | — |

> **用户规则（硬性）：禁止使用 `goto`、禁止使用 `clickat`。**
> 移动只用 `move X Y`；按键只用 `down` + `up`。
> 需要「移动并点击」＝ `move X Y` 然后 `down` `up`。
>
> 注意：`abs` 命名空间下**没有** `down` / `up` / `drag` / `click`；`rel` 下也**没有** `click`；
> `rel` 命名空间下**没有** `goto`。

## 用户明令禁止的动词

| ❌ 禁用 | ✅ 应改为 |
|---|---|
| `goto X Y` | `move X Y` |
| `clickat X Y` | `move X Y` 然后 `down` `up` |
| `click`（作为"移动+点击"的简写） | `move X Y` + `down` + `up` |

## 黑名单（禁止使用 —— 含所有可能的缩写排列组合）

### 一、动词缩写 / 截断形式

| ❌ 禁用 | ✅ 应改为 | 说明 |
|---|---|---|
| `clk` | `click` | 去掉元音 |
| `clk at` / `clkat` | `clickat` 或 `click` + `goto` | 双重缩写 |
| `c` | `click` | 单字母 |
| `g` | `goto` | 单字母 |
| `go` | `goto` | 截断 |
| `gt` | `goto` | 截断 |
| `gto` | `goto` | 去元音 |
| `mv` | `move` | 去元音 |
| `mov` | `move` | 截断 |
| `m` | `move` | 单字母 |
| `dn` | `down` | 去元音 |
| `dwn` | `down` | 去元音 |
| `d` | `down` / `drag` | 单字母且歧义 |
| `u` | `up` | 单字母 |
| `drg` | `drag` | 去元音 |
| `dr` | `drag` | 截断 |
| `wh` / `whl` | `wheel` | 去元音 |
| `w` | `wheel` / `wait` | 单字母且歧义 |
| `wt` / `wai` | `wait` | 截断 |
| `p` / `ps` / `wher` | `pos` | 单字母 / 去元音 |
| `ab` / `a` | `abs` | 截断 |
| `rl` / `r` | `rel` | 去元音 / 单字母 |
| `ms` / `mous` / `mo` | `mouse` | 截断 |

### 二、参数缩写 / 占位符（禁止用于真实命令）

| ❌ 禁用 | ✅ 应改为 |
|---|---|
| `x` `y`（当作字面量） | 具体整数坐标 |
| `dx` `dy`（当作字面量） | 具体整数位移 |
| `n`（当作字面量） | 具体滚轮格数 |
| `t` / `ms`（当作字面量） | 具体毫秒数 |
| `X,Y` / `DX,DY` 逗号拼接 | 空格分隔的两个整数 |

### 三、连写 / 驼峰 / 拼接变体

| ❌ 禁用 | ✅ 应改为 |
|---|---|
| `clickat` 以外的粘连形式：`click-at`, `clickAt`, `ClickAt`, `click_at` | 命令行只用 `clickat`（源码规定）；文本描述用「点击到」 |
| `gotoXY` `moveXY` | `goto X Y` `move X Y` |
| `dragging` `moving` `clicking`（命令位） | `drag` `move` `click` |

### 四、过时 / 文档残留子命令（**源码不存在**）

> `cmd/kvm/main.go` 顶部注释仍写着 `kvm plan` / `kvm fire`，
> 但 `main` 的 `switch` 只接受 `mouse` / `help`。**不要使用 `plan` / `fire`。**

| ❌ 禁用 | ✅ 实际可用 |
|---|---|
| `kvm plan ...` | `kvm -dry-run ...`（预测，不发送） |
| `kvm fire ...` | `kvm ...`（真实发送） |
| `kvm run ...` | 无此子命令 |
| `kvm exec ...` | 无此子命令 |

### 五、其他系统词缩写（本项目语境）

| ❌ 禁用 | ✅ 应改为 |
|---|---|
| `HUD`（口语） | 明确说「十字线叠加层（crosshair overlay）」 |
| `pos` 被口语化成「位置文件」 | `/tmp/kvm-cursor.pos` 全路径 |
| `ACM` / `tty0` | `/dev/ttyACM0` 全路径 |

## 规则总结

1. **只用完整单词**：`goto` `move` `down` `up` `drag` `click` `wheel` `wait` `pos` `mouse` `abs` `rel` `clickat` `help`。
2. **参数必须是具体整数**，不许留 `x`/`dx`/`n` 占位符在真实命令里。
3. **不跨命名空间借用**：`abs` 无按键动词，`rel` 无 `goto`。
4. **不引用源码里不存在的子命令**（`plan` / `fire` 已作废）。
5. 需要"定位并点击"时，唯一合法写法：
   - `kvm mouse abs move X Y` 然后 `kvm mouse rel down` 然后 `kvm mouse rel up`
   - **禁止** `goto`、**禁止** `clickat`、**禁止** `click` 作为简写。
