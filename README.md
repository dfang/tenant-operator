# README

This is extracted from a real project, it use kubernete namespace to isolate resources, use consul as kv store.

the tenants data saved to `tenants/306c7c8e953e2dfd5833/uuid` and `tenants/306c7c8e953e2dfd5833/cname` liked keys. 

## Features:

- One tenant one namespace
- Sleep Mode
- Auto WakeUp
- Traefik as Ingress Controller
- Tenent cmd

## Consul UI

```
brew install consul
open http://localhost:8500/ui

<!-- you may also want to install dnsmasq -->
brew install dnsmasq
```

## Cname domains for tenant

on cloud, you can set your dns records ....

but on local, /etc/hosts doesn't support wildcard records, so add this to dnsmasq.conf

```
address=/jdwl.in/127.0.0.1
```

the url for a tenant is `http://<cname>.jdwl.in`

cname is generated by [haikunator](http://github.com/atrox/haikunatorgo)(a heroku-style random name) when tenant created.


## Tenant cmd

In real scenario, this operator will watch an database table to manipulate a tenant by create/update/delete/sleep Tenant CRD.

this cmd is for demonstration.

```
λ bin/tenant h
NAME:
   tenant - make an explosive entrance

USAGE:
   tenant [global options] command [command options] [arguments...]

COMMANDS:
   list, l         list tenants
   add, a          add a tenant
   update, u       update a tenant cname or replicas
   scale, s        scale replicas of deployment for a tenant by `uuid`
   sleep, sleep    sleep a tenant by `uuid`
   wakeup, wakeup  wakeup a tenant by `uuid`
   delete, d       delete a tenant by `uuid`
   purge, c        purge all tenants
   help, h         Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config FILE  Load configuration from FILE
   --help, -h     show help (default: false)
```


### Some commands maybe helpful when developing on local

```
export $(cat .env.local | xargs)

export $(cat .env| xargs)

kubectl -n pgo port-forward svc/postgres-operator 8443:8443

kubectl -n pgo port-forward svc/tenants 5432:5432

pgo show user tenants --show-system-accounts

pgo delete user --username=solitary-mud-a18aaf3f1 --selector=name=tenants --no-prompt

pgo update user -n pgo tenants --username=hidden-block-645179a57 --password=2d0O/o7(+|#*

```
