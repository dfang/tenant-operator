# package static resources (eg. YAML)

1. github.com/markbates/pkger has BUG (https://github.com/markbates/pkger/issues/134)

2. go 2 embed api not yet ready

3. workround copy controller/templates in Dockerfile


如果build之后单个执行文件随便moved到哪里都能执行，不报找不到template的错误，说明打包成功

How do I use this package? #106

https://github.com/markbates/pkger/issues/106


first pkger
then build

example

https://github.com/markbates/pkger/tree/master/examples/open/pkger


Note:

pkger.Open("/xxxxxxxx")
open 不能传string变量，要写死

因为pkger会通过扫描这个生成pkged.go


如果在子目录可以用完整路径
eg. pkger.Open("github.com/dfang/tenant-operator:/controllers/templates/env-config.yaml")
