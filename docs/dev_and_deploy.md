# Dev

```
kubectl -n pgo port-forward svc/tenants 5432:5432

export $(cat .env.local | xargs)

make deploy
```



# Deploy

```
kubectl create secret generic tenants-db-secret --from-env-file=.env -n jdwl-operator-system

make deploy
```
